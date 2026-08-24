package daemon

import (
	"context"
	"errors"
	"io"
	"net"
	neturl "net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/urltest"
	E "github.com/sagernet/sing/common/exceptions"

	"google.golang.org/protobuf/types/known/emptypb"
)

const (
	defaultURLTestTimeout     = 5 * time.Second
	minimumURLTestTimeout     = 500 * time.Millisecond
	maximumURLTestTimeout     = 30 * time.Second
	defaultURLTestDeadline    = 30 * time.Second
	maximumURLTestDeadline    = 2 * time.Minute
	defaultURLTestConcurrency = 8
	maximumURLTestConcurrency = 16
)

type urlTestSession struct {
	id       uint64
	instance *Instance
	cancel   context.CancelFunc
}

type urlTestSessionOptions struct {
	link        string
	timeout     time.Duration
	deadline    time.Duration
	concurrency int
}

type urlTestTarget struct {
	tag      string
	outbound adapter.Outbound
}

type urlTestProbe func(context.Context, string, adapter.Outbound) (uint16, error)
type urlTestResultHandler func(urlTestTarget, uint16, error)

func (s *StartedService) startURLTest(request *URLTestRequest) (*emptypb.Empty, error) {
	if request == nil {
		return nil, E.New("missing URL test request")
	}
	groupTag := strings.TrimSpace(request.OutboundTag)
	if groupTag == "" {
		return nil, E.New("missing outbound group tag")
	}
	options := normalizeURLTestOptions(request)
	if err := validateURLTestLink(options.link); err != nil {
		return nil, err
	}

	s.serviceAccess.RLock()
	if s.serviceStatus.Status != ServiceStatus_STARTED || s.instance == nil {
		s.serviceAccess.RUnlock()
		return nil, os.ErrInvalid
	}
	boxService := s.instance
	targets, err := resolveURLTestTargets(
		boxService,
		groupTag,
		strings.TrimSpace(request.TargetOutboundTag),
		strings.TrimSpace(request.PriorityOutboundTag),
		strings.TrimSpace(request.ExcludeOutboundTag),
	)
	if err != nil {
		s.serviceAccess.RUnlock()
		return nil, err
	}
	sessionContext, cancel := context.WithTimeout(boxService.ctx, options.deadline)

	s.urlTestSessionAccess.Lock()
	if existing := s.urlTestSessions[groupTag]; existing != nil {
		if existing.instance == boxService && !request.Force {
			s.urlTestSessionAccess.Unlock()
			s.serviceAccess.RUnlock()
			cancel()
			return &emptypb.Empty{}, nil
		}
		existing.cancel()
	}
	s.urlTestSessionSequence++
	session := &urlTestSession{
		id:       s.urlTestSessionSequence,
		instance: boxService,
		cancel:   cancel,
	}
	s.urlTestSessions[groupTag] = session
	s.urlTestSessionAccess.Unlock()
	s.serviceAccess.RUnlock()

	go s.runURLTestSession(sessionContext, groupTag, session, targets, options)
	return &emptypb.Empty{}, nil
}

func normalizeURLTestOptions(request *URLTestRequest) urlTestSessionOptions {
	timeout := clampDuration(
		durationFromMilliseconds(request.TimeoutMillis, defaultURLTestTimeout),
		minimumURLTestTimeout,
		maximumURLTestTimeout,
	)
	deadline := clampDuration(
		durationFromMilliseconds(request.DeadlineMillis, defaultURLTestDeadline),
		timeout,
		maximumURLTestDeadline,
	)
	concurrency := int(request.Concurrency)
	if concurrency <= 0 {
		concurrency = defaultURLTestConcurrency
	} else if concurrency > maximumURLTestConcurrency {
		concurrency = maximumURLTestConcurrency
	}
	return urlTestSessionOptions{
		link:        strings.TrimSpace(request.UrlTestUrl),
		timeout:     timeout,
		deadline:    deadline,
		concurrency: concurrency,
	}
}

func durationFromMilliseconds(value int32, fallback time.Duration) time.Duration {
	if value <= 0 {
		return fallback
	}
	return time.Duration(value) * time.Millisecond
}

func clampDuration(value time.Duration, minimum time.Duration, maximum time.Duration) time.Duration {
	if value < minimum {
		return minimum
	}
	if value > maximum {
		return maximum
	}
	return value
}

func validateURLTestLink(link string) error {
	if link == "" {
		return nil
	}
	parsed, err := neturl.Parse(link)
	if err != nil {
		return E.Cause(err, "invalid URL test URL")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return E.New("URL test URL must use http or https")
	}
	if parsed.Hostname() == "" {
		return E.New("URL test URL must contain a host")
	}
	return nil
}

func resolveURLTestTargets(boxService *Instance, groupTag string, targetTag string, priorityTag string, excludeTag string) ([]urlTestTarget, error) {
	outboundManager := boxService.outboundManager
	rootOutbound, loaded := outboundManager.Outbound(groupTag)
	if !loaded {
		return nil, E.New("outbound group not found: ", groupTag)
	}
	rootGroup, isGroup := rootOutbound.(adapter.OutboundGroup)
	if !isGroup {
		return nil, E.New("outbound is not a group: ", groupTag)
	}

	targets, err := collectConcreteURLTestTargets(outboundManager, rootGroup.All())
	if err != nil {
		return nil, err
	}
	memberIndex := indexURLTestTargets(targets)

	if excludeTag != "" {
		excluded := make(map[string]bool)
		if excludeOutbound, found := outboundManager.Outbound(excludeTag); found {
			if excludeGroup, groupFound := excludeOutbound.(adapter.OutboundGroup); groupFound {
				excludeTargets, collectErr := collectConcreteURLTestTargets(outboundManager, excludeGroup.All())
				if collectErr != nil {
					return nil, collectErr
				}
				for _, excludeTarget := range excludeTargets {
					excluded[excludeTarget.tag] = true
				}
			} else {
				excluded[excludeOutbound.Tag()] = true
			}
		}
		if len(excluded) > 0 {
			filtered := targets[:0]
			for _, candidate := range targets {
				if !excluded[candidate.tag] {
					filtered = append(filtered, candidate)
				}
			}
			targets = filtered
			memberIndex = indexURLTestTargets(targets)
		}
	}

	if targetTag != "" {
		targetOutbound, resolveErr := resolveSelectedURLTestOutbound(outboundManager, targetTag)
		if resolveErr != nil {
			return nil, resolveErr
		}
		index, isMember := memberIndex[targetOutbound.Tag()]
		if !isMember {
			return nil, E.New("target outbound is not a member of group ", groupTag, ": ", targetTag)
		}
		return []urlTestTarget{targets[index]}, nil
	}

	if priorityTag != "" {
		if priorityOutbound, resolveErr := resolveSelectedURLTestOutbound(outboundManager, priorityTag); resolveErr == nil {
			if index, isMember := memberIndex[priorityOutbound.Tag()]; isMember && index > 0 {
				priorityTarget := targets[index]
				copy(targets[1:index+1], targets[:index])
				targets[0] = priorityTarget
			}
		}
	}
	if len(targets) == 0 {
		return nil, E.New("outbound group has no testable members: ", groupTag)
	}
	return targets, nil
}

func indexURLTestTargets(targets []urlTestTarget) map[string]int {
	index := make(map[string]int, len(targets))
	for targetIndex, target := range targets {
		index[target.tag] = targetIndex
	}
	return index
}

func collectConcreteURLTestTargets(outboundManager adapter.OutboundManager, tags []string) ([]urlTestTarget, error) {
	seen := make(map[string]bool)
	visiting := make(map[string]bool)
	var targets []urlTestTarget
	var visit func(string) error
	visit = func(tag string) error {
		outbound, loaded := outboundManager.Outbound(tag)
		if !loaded {
			return E.New("outbound not found: ", tag)
		}
		if outboundGroup, isGroup := outbound.(adapter.OutboundGroup); isGroup {
			if visiting[tag] {
				return E.New("cyclic outbound group reference: ", tag)
			}
			visiting[tag] = true
			for _, childTag := range outboundGroup.All() {
				if err := visit(childTag); err != nil {
					return err
				}
			}
			delete(visiting, tag)
			return nil
		}
		realTag := outbound.Tag()
		if seen[realTag] {
			return nil
		}
		seen[realTag] = true
		targets = append(targets, urlTestTarget{tag: realTag, outbound: outbound})
		return nil
	}
	for _, tag := range tags {
		if err := visit(tag); err != nil {
			return nil, err
		}
	}
	return targets, nil
}

func resolveSelectedURLTestOutbound(outboundManager adapter.OutboundManager, tag string) (adapter.Outbound, error) {
	visited := make(map[string]bool)
	for {
		outbound, loaded := outboundManager.Outbound(tag)
		if !loaded {
			return nil, E.New("outbound not found: ", tag)
		}
		outboundGroup, isGroup := outbound.(adapter.OutboundGroup)
		if !isGroup {
			return outbound, nil
		}
		if visited[tag] {
			return nil, E.New("cyclic outbound group selection: ", tag)
		}
		visited[tag] = true
		tag = strings.TrimSpace(outboundGroup.Now())
		if tag == "" {
			return nil, E.New("outbound group has no selected member: ", outboundGroup.Tag())
		}
	}
}

func (s *StartedService) runURLTestSession(ctx context.Context, groupTag string, session *urlTestSession, targets []urlTestTarget, options urlTestSessionOptions) {
	defer session.cancel()
	defer s.finishURLTestSession(groupTag, session)

	probe := func(probeContext context.Context, link string, outbound adapter.Outbound) (uint16, error) {
		return urltest.URLTest(probeContext, link, outbound)
	}
	runURLTestTargets(ctx, targets, options, probe, func(target urlTestTarget, delay uint16, err error) {
		if !s.isCurrentURLTestSession(groupTag, session) {
			return
		}
		now := time.Now()
		if err != nil {
			errorCode, errorMessage := classifyURLTestError(err)
			session.instance.urlTestHistoryStorage.StoreURLTestHistory(target.tag, &adapter.URLTestHistory{
				Time:      now,
				Status:    adapter.URLTestStatusUnavailable,
				Error:     errorMessage,
				ErrorCode: errorCode,
			})
			return
		}
		if delay == 0 {
			delay = 1
		}
		session.instance.urlTestHistoryStorage.StoreURLTestHistory(target.tag, &adapter.URLTestHistory{
			Time:   now,
			Delay:  delay,
			Status: adapter.URLTestStatusAvailable,
		})
	})
}

func runURLTestTargets(ctx context.Context, targets []urlTestTarget, options urlTestSessionOptions, probe urlTestProbe, handleResult urlTestResultHandler) {
	if len(targets) == 0 || probe == nil || handleResult == nil {
		return
	}
	workerCount := options.concurrency
	if workerCount <= 0 {
		workerCount = 1
	}
	if workerCount > len(targets) {
		workerCount = len(targets)
	}
	jobs := make(chan urlTestTarget, len(targets))
	for _, target := range targets {
		jobs <- target
	}
	close(jobs)

	var workers sync.WaitGroup
	workers.Add(workerCount)
	for range workerCount {
		go func() {
			defer workers.Done()
			for {
				select {
				case <-ctx.Done():
					return
				case target, loaded := <-jobs:
					if !loaded || ctx.Err() != nil {
						return
					}
					probeContext, cancel := context.WithTimeout(ctx, options.timeout)
					delay, err := probe(probeContext, options.link, target.outbound)
					contextErr := probeContext.Err()
					cancel()
					if err == nil && contextErr != nil {
						err = contextErr
					}
					handleResult(target, delay, err)
				}
			}
		}()
	}
	workers.Wait()
}

func (s *StartedService) isCurrentURLTestSession(groupTag string, session *urlTestSession) bool {
	s.urlTestSessionAccess.Lock()
	defer s.urlTestSessionAccess.Unlock()
	return s.urlTestSessions[groupTag] == session
}

func (s *StartedService) finishURLTestSession(groupTag string, session *urlTestSession) {
	s.urlTestSessionAccess.Lock()
	if s.urlTestSessions[groupTag] == session {
		delete(s.urlTestSessions, groupTag)
	}
	s.urlTestSessionAccess.Unlock()
}

func classifyURLTestError(err error) (string, string) {
	if err == nil {
		return "", ""
	}
	for {
		urlError, isURLError := err.(*neturl.Error)
		if !isURLError || urlError.Err == nil {
			break
		}
		err = urlError.Err
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout", "request timed out"
	}
	if errors.Is(err, context.Canceled) {
		return "cancelled", "request cancelled"
	}
	var dnsError *net.DNSError
	if errors.As(err, &dnsError) {
		return "dns", safeProbeError(err)
	}
	var networkError net.Error
	if errors.As(err, &networkError) && networkError.Timeout() {
		return "timeout", "request timed out"
	}
	if errors.Is(err, io.EOF) || strings.Contains(strings.ToLower(err.Error()), "unexpected eof") {
		return "eof", "connection closed before a response was received"
	}
	lowerMessage := strings.ToLower(err.Error())
	if strings.Contains(lowerMessage, "tls") || strings.Contains(lowerMessage, "certificate") || strings.Contains(lowerMessage, "x509") {
		return "tls", safeProbeError(err)
	}
	return "network", safeProbeError(err)
}

func safeProbeError(err error) string {
	message := strings.Join(strings.Fields(err.Error()), " ")
	const maximumRunes = 240
	runes := []rune(message)
	if len(runes) > maximumRunes {
		message = string(runes[:maximumRunes]) + "…"
	}
	if message == "" {
		return "network request failed"
	}
	return message
}
