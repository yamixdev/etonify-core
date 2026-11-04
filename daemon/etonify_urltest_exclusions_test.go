package daemon

import (
	"context"
	box "github.com/sagernet/sing-box"
	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/adapter/certificate"
	"github.com/sagernet/sing-box/adapter/endpoint"
	"github.com/sagernet/sing-box/adapter/inbound"
	"github.com/sagernet/sing-box/adapter/outbound"
	serviceAdapter "github.com/sagernet/sing-box/adapter/service"
	"github.com/sagernet/sing-box/common/urltest"
	"github.com/sagernet/sing-box/dns"
	"github.com/sagernet/sing-box/dns/transport/local"
	"github.com/sagernet/sing-box/protocol/direct"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"testing"
	"time"
)

func TestConfiguredExclusionsExistBeforeRuntimeStarts(t *testing.T) {
	outbounds := outbound.NewRegistry()
	direct.RegisterOutbound(outbounds)
	transports := dns.NewTransportRegistry()
	local.RegisterTransport(transports)
	ctx := box.Context(context.Background(), inbound.NewRegistry(), outbounds, endpoint.NewRegistry(), transports, serviceAdapter.NewRegistry(), certificate.NewRegistry())
	s := NewStartedService(ServiceOptions{Context: ctx, LogMaxLines: 32})
	i, err := s.newInstance(ctx, `{"outbounds":[{"type":"direct","tag":"a"},{"type":"direct","tag":"b"}],"experimental":{"url_test_excluded_tags":["a"]}}`, nil, false)
	require.NoError(t, err)
	defer i.Close()
	require.True(t, i.urlTestHistoryStorage.IsExcluded("a"))
	require.False(t, i.urlTestHistoryStorage.IsExcluded("b"))
}

type exclusionTestManager struct{ selectionTestManager }

func (m exclusionTestManager) Outbounds() []adapter.Outbound {
	var result []adapter.Outbound
	for _, o := range m.outbounds {
		result = append(result, o)
	}
	return result
}

func TestLiveExclusionsRejectStaleRuntimeAndReplaceSnapshot(t *testing.T) {
	manager := exclusionTestManager{selectionTestManager{outbounds: map[string]adapter.Outbound{"a": mockLeafOutbound{tag: "a"}}}}
	h := urltest.NewHistoryStorage()
	s := &StartedService{serviceStatus: &ServiceStatus{Status: ServiceStatus_STARTED}, startedAt: time.UnixMilli(123), instance: &Instance{outboundManager: manager, urlTestHistoryStorage: h}}
	probeContext, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.urlTestSessions = map[string]*urlTestSession{"auto": {instance: s.instance, ctx: probeContext, cancel: cancel}}
	_, err := s.SetURLTestExcludedOutbounds(context.Background(), &URLTestExcludedOutboundsRequest{ExpectedStartedAt: 122, ExcludedTags: []string{"a"}})
	require.Equal(t, codes.FailedPrecondition, status.Code(err))
	require.False(t, h.IsExcluded("a"))
	_, err = s.SetURLTestExcludedOutbounds(context.Background(), &URLTestExcludedOutboundsRequest{ExpectedStartedAt: 123, ExcludedTags: []string{"a"}})
	require.NoError(t, err)
	require.True(t, h.IsExcluded("a"))
	require.Error(t, probeContext.Err(), "in-flight checks were not cancelled before apply returned")
	_, err = s.SetURLTestExcludedOutbounds(context.Background(), &URLTestExcludedOutboundsRequest{ExpectedStartedAt: 123})
	require.NoError(t, err)
	require.False(t, h.IsExcluded("a"))
	_, err = s.SetURLTestExcludedOutbounds(context.Background(), &URLTestExcludedOutboundsRequest{ExpectedStartedAt: 123, ExcludedTags: []string{"missing"}})
	require.Equal(t, codes.InvalidArgument, status.Code(err))
	require.False(t, h.IsExcluded("a"))
}
