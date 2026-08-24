package option

import (
	"bytes"
	"strconv"
	"strings"

	E "github.com/sagernet/sing/common/exceptions"
	"github.com/sagernet/sing/common/json"
	"github.com/sagernet/sing/common/json/badoption"
)

// V2RayXHTTPRangeConfig represents a range with from/to values
type V2RayXHTTPRangeConfig struct {
	From int32 `json:"from,omitempty"`
	To   int32 `json:"to,omitempty"`
}

func (r *V2RayXHTTPRangeConfig) UnmarshalJSON(content []byte) error {
	if bytes.Equal(content, []byte("null")) {
		return nil
	}
	if len(content) > 1 && content[0] == '"' {
		var value string
		if err := json.Unmarshal(content, &value); err != nil {
			return err
		}
		from, to, err := parseV2RayXHTTPRange(value)
		if err != nil {
			return err
		}
		r.From = from
		r.To = to
		return nil
	}
	if len(content) > 0 && content[0] != '{' {
		var value int32
		if err := json.Unmarshal(content, &value); err != nil {
			return err
		}
		r.From = value
		r.To = value
		return nil
	}
	type plain V2RayXHTTPRangeConfig
	return json.Unmarshal(content, (*plain)(r))
}

func parseV2RayXHTTPRange(value string) (int32, int32, error) {
	value = strings.TrimSpace(value)
	if scalar, err := strconv.ParseInt(value, 10, 32); err == nil {
		return int32(scalar), int32(scalar), nil
	}
	for index := 1; index < len(value); index++ {
		if value[index] != '-' {
			continue
		}
		from, fromErr := strconv.ParseInt(strings.TrimSpace(value[:index]), 10, 32)
		to, toErr := strconv.ParseInt(strings.TrimSpace(value[index+1:]), 10, 32)
		if fromErr == nil && toErr == nil {
			return int32(from), int32(to), nil
		}
	}
	return 0, 0, E.New("invalid xhttp range: ", value)
}

// V2RayXHTTPXmuxConfig represents xmux (connection multiplexing) settings
type V2RayXHTTPXmuxConfig struct {
	MaxConcurrency   *V2RayXHTTPRangeConfig `json:"max_concurrency,omitempty"`
	MaxConnections   *V2RayXHTTPRangeConfig `json:"max_connections,omitempty"`
	CMaxReuseTimes   *V2RayXHTTPRangeConfig `json:"c_max_reuse_times,omitempty"`
	HMaxRequestTimes *V2RayXHTTPRangeConfig `json:"h_max_request_times,omitempty"`
	HMaxReusableSecs *V2RayXHTTPRangeConfig `json:"h_max_reusable_secs,omitempty"`
	HKeepAlivePeriod int64                  `json:"h_keep_alive_period,omitempty"`
}

type V2RayXHTTPOptions struct {
	Host    string               `json:"host,omitempty"`
	Path    string               `json:"path,omitempty"`
	Mode    string               `json:"mode,omitempty"`
	Headers badoption.HTTPHeader `json:"headers,omitempty"`

	// Padding settings
	XPaddingBytes     *V2RayXHTTPRangeConfig `json:"x_padding_bytes,omitempty"`
	XPaddingObfsMode  bool                   `json:"x_padding_obfs_mode,omitempty"`
	XPaddingKey       string                 `json:"x_padding_key,omitempty"`
	XPaddingHeader    string                 `json:"x_padding_header,omitempty"`
	XPaddingPlacement string                 `json:"x_padding_placement,omitempty"`
	XPaddingMethod    string                 `json:"x_padding_method,omitempty"`

	// Response headers
	NoGRPCHeader bool `json:"no_grpc_header,omitempty"`
	NoSSEHeader  bool `json:"no_sse_header,omitempty"`

	// Upload settings
	UplinkHTTPMethod    string                 `json:"uplink_http_method,omitempty"`
	UplinkDataPlacement string                 `json:"uplink_data_placement,omitempty"`
	UplinkDataKey       string                 `json:"uplink_data_key,omitempty"`
	UplinkChunkSize     *V2RayXHTTPRangeConfig `json:"uplink_chunk_size,omitempty"`

	// Session/seq placement
	SessionPlacement string                 `json:"session_placement,omitempty"`
	SessionKey       string                 `json:"session_key,omitempty"`
	SessionTable     string                 `json:"session_table,omitempty"`
	SessionLength    *V2RayXHTTPRangeConfig `json:"session_length,omitempty"`
	SeqPlacement     string                 `json:"seq_placement,omitempty"`
	SeqKey           string                 `json:"seq_key,omitempty"`

	// Packet-up mode settings
	ScMaxEachPostBytes   *V2RayXHTTPRangeConfig `json:"sc_max_each_post_bytes,omitempty"`
	ScMinPostsIntervalMs *V2RayXHTTPRangeConfig `json:"sc_min_posts_interval_ms,omitempty"`
	ScMaxBufferedPosts   int                    `json:"sc_max_buffered_posts,omitempty"`

	// Stream-up server response interval
	ScStreamUpServerSecs *V2RayXHTTPRangeConfig `json:"sc_stream_up_server_secs,omitempty"`

	ServerMaxHeaderBytes int32 `json:"server_max_header_bytes,omitempty"`

	// Xmux settings (client-side connection multiplexing)
	Xmux *V2RayXHTTPXmuxConfig `json:"xmux,omitempty"`
}
