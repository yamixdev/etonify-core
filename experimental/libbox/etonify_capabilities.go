package libbox

import (
	"encoding/json"

	C "github.com/sagernet/sing-box/constant"
)

const etonifyAPIVersion = 1

type etonifyCapabilitySet struct {
	APIVersion                              int      `json:"api_version"`
	CoreVersion                             string   `json:"core_version"`
	SupportsTargetedURLTest                 bool     `json:"supports_targeted_url_test"`
	SupportsGroupURLTestSessions            bool     `json:"supports_group_url_test_sessions"`
	SupportsStructuredProbeErrors           bool     `json:"supports_structured_probe_errors"`
	SupportsOutboundExternalInfo            bool     `json:"supports_outbound_external_info"`
	SupportsMixedRoutingOutbound            bool     `json:"supports_mixed_routing_outbound"`
	SupportsURLTestTimeout                  bool     `json:"supports_url_test_timeout"`
	SupportsURLTestConcurrency              bool     `json:"supports_url_test_concurrency"`
	SupportsURLTestDeadline                 bool     `json:"supports_url_test_deadline"`
	SupportsURLTestForce                    bool     `json:"supports_url_test_force"`
	SupportsURLTestUnavailableCheckInterval bool     `json:"supports_url_test_unavailable_check_interval"`
	SupportsURLTestMethod                   bool     `json:"supports_url_test_method"`
	SupportsURLTestInterruptDelayThreshold  bool     `json:"supports_url_test_interrupt_delay_threshold"`
	URLTestCompletionModel                  string   `json:"url_test_completion_model"`
	SupportsConfigCheck                     bool     `json:"supports_config_check"`
	SupportsCloseConnections                bool     `json:"supports_close_connections"`
	TUNStacks                               []string `json:"tun_stacks"`
}

// EtonifyCapabilities returns the versioned mobile integration contract.
//
// The JSON representation allows newer cores to add optional capabilities
// without forcing older clients to bind newly introduced Go types. A client
// must treat a missing or malformed response as the legacy capability set.
func EtonifyCapabilities() string {
	capabilities := etonifyCapabilitySet{
		APIVersion:                    etonifyAPIVersion,
		CoreVersion:                   C.Version,
		SupportsTargetedURLTest:       true,
		SupportsGroupURLTestSessions:  true,
		SupportsStructuredProbeErrors: true,
		SupportsOutboundExternalInfo:  true,
		SupportsURLTestTimeout:        true,
		SupportsURLTestConcurrency:    true,
		SupportsURLTestDeadline:       true,
		SupportsURLTestForce:          true,
		URLTestCompletionModel:        "group_events",
		SupportsConfigCheck:           true,
		SupportsCloseConnections:      true,
		TUNStacks:                     []string{"system", "gvisor", "mixed"},
	}
	content, err := json.Marshal(capabilities)
	if err != nil {
		return ""
	}
	return string(content)
}
