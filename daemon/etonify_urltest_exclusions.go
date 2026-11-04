package daemon

import (
	"context"
	"strings"

	"github.com/sagernet/sing-box/adapter"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
)

func (s *StartedService) SetURLTestExcludedOutbounds(ctx context.Context, request *URLTestExcludedOutboundsRequest) (*emptypb.Empty, error) {
	if request == nil || request.ExpectedStartedAt <= 0 {
		return nil, status.Error(codes.InvalidArgument, "expected_started_at is required")
	}
	s.serviceAccess.RLock()
	defer s.serviceAccess.RUnlock()
	if s.instance == nil || s.serviceStatus == nil || s.serviceStatus.Status != ServiceStatus_STARTED || s.startedAt.UnixMilli() != request.ExpectedStartedAt {
		return nil, status.Error(codes.FailedPrecondition, "runtime changed")
	}
	instance := s.instance
	tags := make([]string, 0, len(request.ExcludedTags))
	for _, tag := range request.ExcludedTags {
		if tag == "" || strings.TrimSpace(tag) != tag {
			return nil, status.Error(codes.InvalidArgument, "invalid excluded leaf tag")
		}
		outbound, found := instance.outboundManager.Outbound(tag)
		if !found {
			return nil, status.Error(codes.InvalidArgument, "excluded outbound not found: "+tag)
		}
		if _, isGroup := outbound.(adapter.OutboundGroup); isGroup {
			return nil, status.Error(codes.InvalidArgument, "excluded tag must name a leaf: "+tag)
		}
		tags = append(tags, tag)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	instance.urlTestHistoryStorage.SetExcludedTags(tags)
	s.urlTestSessionAccess.Lock()
	for _, session := range s.urlTestSessions {
		if session.instance == instance {
			session.requestCancel("eligibility_changed")
		}
	}
	s.urlTestSessionAccess.Unlock()
	// Every group shares this instance's storage; nested selection consumes the
	// new snapshot immediately, and refresh never interrupts attached flows.
	for _, outbound := range instance.outboundManager.Outbounds() {
		if group, ok := outbound.(interface{ RefreshURLTestEligibility() }); ok {
			group.RefreshURLTestEligibility()
		}
	}
	return &emptypb.Empty{}, nil
}
