package v2rayhttp

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/sagernet/sing-box/common/tls"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/stretchr/testify/require"
)

func TestRawTCPHTTPHeader(t *testing.T) {
	for _, name := range []string{"plain", "tls"} {
		t.Run(name, func(t *testing.T) {
			deadline := time.Now().Add(5 * time.Second)
			ctx, cancel := context.WithDeadline(context.Background(), deadline)
			defer cancel()
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.RequestURI != "/mask?query=value" || r.Proto != "HTTP/1.1" || r.Host != "cover.example" {
					t.Errorf("unexpected TCP header: %s %s %s host=%s", r.Method, r.RequestURI, r.Proto, r.Host)
				}
				if mask := r.Header.Get("X-Mask"); mask != "one" && mask != "two" {
					t.Errorf("unexpected X-Mask value %q", mask)
				}
				conn, buffered, err := w.(http.Hijacker).Hijack()
				if err != nil {
					t.Error(err)
					return
				}
				defer conn.Close()
				if err := conn.SetDeadline(deadline); err != nil {
					t.Error(err)
					return
				}
				var payload [4]byte
				if _, err := io.ReadFull(buffered, payload[:]); err != nil || string(payload[:]) != "ping" {
					t.Errorf("unexpected raw payload %q: %v", payload, err)
					return
				}
				if _, err := io.WriteString(conn, "HTTP/1.1 204 No Content\r\n\r\npong"); err != nil {
					t.Error(err)
				}
			}))
			var tlsConfig tls.Config
			if name == "tls" {
				server.StartTLS()
				var err error
				tlsConfig, err = tls.NewClient(ctx, log.NewNOPFactory().Logger(), "127.0.0.1", option.OutboundTLSOptions{Enabled: true, Insecure: true})
				require.NoError(t, err)
			} else {
				server.Start()
			}
			defer server.Close()
			_, serverAddress, _ := strings.Cut(server.URL, "://")
			var transport option.V2RayTransportOptions
			require.NoError(t, json.Unmarshal([]byte(`{"type":"http","raw_tcp":true,"host":["cover.example"],"path":"/mask?query=value","headers":{"X-Mask":["one","two"]}}`), &transport))
			client, err := NewClient(ctx, N.SystemDialer, M.ParseSocksaddr(serverAddress), transport.HTTPOptions, tlsConfig)
			require.NoError(t, err)
			defer client.Close()
			require.False(t, client.(*Client).MultiplexEnabled())
			conn, err := client.DialContext(ctx)
			require.NoError(t, err)
			defer conn.Close()
			require.NoError(t, conn.SetDeadline(deadline))
			_, err = io.WriteString(conn, "ping")
			require.NoError(t, err)
			var payload [4]byte
			_, err = io.ReadFull(conn, payload[:])
			require.NoError(t, err)
			require.Equal(t, "pong", string(payload[:]))
		})
	}
}
