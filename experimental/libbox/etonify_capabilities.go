package libbox

import (
	"encoding/json"

	C "github.com/sagernet/sing-box/constant"
)

// API v2 makes the detailed XHTTP and VLESS Encryption limits part of the
// mobile integration contract. Clients must not infer these limits from the
// coarse supports_* flags.
const etonifyAPIVersion = 4

type etonifyCapabilitySet struct {
	APIVersion                              int      `json:"api_version"`
	CoreVersion                             string   `json:"core_version"`
	SupportsTargetedURLTest                 bool     `json:"supports_targeted_url_test"`
	SupportsURLTestQueuePriority            bool     `json:"supports_url_test_queue_priority"`
	SupportsGroupURLTestSessions            bool     `json:"supports_group_url_test_sessions"`
	SupportsStructuredProbeErrors           bool     `json:"supports_structured_probe_errors"`
	SupportsOutboundExternalInfo            bool     `json:"supports_outbound_external_info"`
	SupportsOutboundHTTPFetch               bool     `json:"supports_outbound_http_fetch"`
	SupportsMixedRoutingOutbound            bool     `json:"supports_mixed_routing_outbound"`
	SupportsURLTestTimeout                  bool     `json:"supports_url_test_timeout"`
	SupportsURLTestConcurrency              bool     `json:"supports_url_test_concurrency"`
	SupportsURLTestDeadline                 bool     `json:"supports_url_test_deadline"`
	SupportsURLTestForce                    bool     `json:"supports_url_test_force"`
	SupportsURLTestFailover                 bool     `json:"supports_url_test_failover"`
	SupportsURLTestUnavailableCheckInterval bool     `json:"supports_url_test_unavailable_check_interval"`
	SupportsURLTestMethod                   bool     `json:"supports_url_test_method"`
	SupportsURLTestInterruptDelayThreshold  bool     `json:"supports_url_test_interrupt_delay_threshold"`
	URLTestCompletionModel                  string   `json:"url_test_completion_model"`
	SupportsURLTestDeltaStream              bool     `json:"supports_url_test_delta_stream"`
	SupportsURLTestSessionStatus            bool     `json:"supports_url_test_session_status"`
	SupportsURLTestResultRevision           bool     `json:"supports_url_test_result_revision"`
	SupportsURLTestNetworkGeneration        bool     `json:"supports_url_test_network_generation"`
	SupportsURLTestExhaustive               bool     `json:"supports_url_test_exhaustive"`
	SupportsURLTestCancel                   bool     `json:"supports_url_test_cancel"`
	SupportsURLTestHandoff                  bool     `json:"supports_url_test_handoff"`
	SupportsConfigCheck                     bool     `json:"supports_config_check"`
	SupportsCloseConnections                bool     `json:"supports_close_connections"`
	SupportsRealitySpiderX                  bool     `json:"supports_reality_spider_x"`
	SupportsXHTTP                           bool     `json:"supports_xhttp"`
	SupportsSplitHTTPAlias                  bool     `json:"supports_splithttp_alias"`
	XHTTPClientOnly                         bool     `json:"xhttp_client_only"`
	XHTTPProfile                            string   `json:"xhttp_profile"`
	XHTTPModes                              []string `json:"xhttp_modes"`
	XHTTPMaxPoolConnections                 int      `json:"xhttp_max_pool_connections"`
	XHTTPMaxPacketUploadBytes               int      `json:"xhttp_max_packet_upload_bytes"`
	SupportsXHTTPCloseAll                   bool     `json:"supports_xhttp_close_all"`
	SupportsVLESSEncryption                 bool     `json:"supports_vless_encryption"`
	VLESSEncryptionClientOnly               bool     `json:"vless_encryption_client_only"`
	VLESSEncryptionModes                    []string `json:"vless_encryption_modes"`
	VLESSEncryptionMaxRelays                int      `json:"vless_encryption_max_relays"`
	VLESSEncryptionHandshakeTimeoutMS       int      `json:"vless_encryption_handshake_timeout_ms"`
	TUNStacks                               []string `json:"tun_stacks"`
}

// EtonifyCapabilities returns the versioned mobile integration contract.
//
// The JSON representation allows newer cores to add optional capabilities
// without forcing older clients to bind newly introduced Go types. A client
// must treat a missing or malformed response as the legacy capability set.
func EtonifyCapabilities() string {
	capabilities := etonifyCapabilitySet{
		APIVersion:                        etonifyAPIVersion,
		CoreVersion:                       C.Version,
		SupportsTargetedURLTest:           true,
		SupportsURLTestQueuePriority:      true,
		SupportsGroupURLTestSessions:      true,
		SupportsStructuredProbeErrors:     true,
		SupportsOutboundExternalInfo:      true,
		SupportsOutboundHTTPFetch:         true,
		SupportsURLTestTimeout:            true,
		SupportsURLTestConcurrency:        true,
		SupportsURLTestDeadline:           true,
		SupportsURLTestForce:              true,
		SupportsURLTestFailover:           true,
		URLTestCompletionModel:            "session_events",
		SupportsURLTestDeltaStream:        true,
		SupportsURLTestSessionStatus:      true,
		SupportsURLTestResultRevision:     true,
		SupportsURLTestNetworkGeneration:  true,
		SupportsURLTestExhaustive:         true,
		SupportsURLTestCancel:             true,
		SupportsURLTestHandoff:            true,
		SupportsConfigCheck:               true,
		SupportsCloseConnections:          true,
		SupportsRealitySpiderX:            true,
		SupportsXHTTP:                     true,
		SupportsSplitHTTPAlias:            true,
		XHTTPClientOnly:                   true,
		XHTTPProfile:                      "etonify_client_v1",
		XHTTPModes:                        []string{"packet-up", "stream-up", "stream-one"},
		XHTTPMaxPoolConnections:           16,
		XHTTPMaxPacketUploadBytes:         256 * 1024,
		SupportsXHTTPCloseAll:             true,
		SupportsVLESSEncryption:           true,
		VLESSEncryptionClientOnly:         true,
		VLESSEncryptionModes:              []string{"1rtt", "0rtt", "native", "xorpub", "random", "x25519", "mlkem768"},
		VLESSEncryptionMaxRelays:          8,
		VLESSEncryptionHandshakeTimeoutMS: 12_000,
		// "native" means the sing-tun stack introduced in sing-box 1.15. It is
		// selected by omitting the deprecated TUN `stack` field. "system"
		// remains available as an explicit compatibility mode.
		TUNStacks: []string{"native", "system"},
	}
	content, err := json.Marshal(capabilities)
	if err != nil {
		return ""
	}
	return string(content)
}
