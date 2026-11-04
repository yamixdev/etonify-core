package device

import (
	"context"
	"net"
	"net/netip"
	"testing"
	"time"

	"github.com/sagernet/sing-box/log"
	M "github.com/sagernet/sing/common/metadata"
	"github.com/stretchr/testify/require"
)

func TestStackAddressChangeClosesOnlyChangedFamilyUDP(t *testing.T) {
	for _, dial := range []bool{false, true} {
		t.Run(map[bool]string{false: "listen", true: "dial"}[dial], func(t *testing.T) {
			addresses := []netip.Prefix{netip.MustParsePrefix("192.0.2.1/32"), netip.MustParsePrefix("fd00::1/128")}
			d, err := newStackDevice(Options{Context: context.Background(), Logger: log.NewNOPFactory().Logger(), MTU: 1500, Configuration: Configuration{Address: addresses}})
			require.NoError(t, err)
			t.Cleanup(func() { _ = d.Close() })
			require.NoError(t, d.Start())
			open := func(destination string) (net.PacketConn, net.Conn) {
				if dial {
					c, err := d.DialContext(context.Background(), "udp", M.ParseSocksaddr(destination))
					require.NoError(t, err)
					t.Cleanup(func() { _ = c.Close() })
					return nil, c
				}
				c, err := d.ListenPacket(context.Background(), M.ParseSocksaddr(destination))
				require.NoError(t, err)
				t.Cleanup(func() { _ = c.Close() })
				return c, nil
			}
			p4, c4 := open("192.0.2.2:53")
			p6, c6 := open("[fd00::2]:53")
			read := func(p net.PacketConn, c net.Conn) error {
				if c != nil {
					_ = c.SetReadDeadline(time.Now().Add(30 * time.Millisecond))
					_, err := c.Read(make([]byte, 1))
					return err
				}
				_ = p.SetReadDeadline(time.Now().Add(30 * time.Millisecond))
				_, _, err := p.ReadFrom(make([]byte, 1))
				return err
			}
			require.NoError(t, d.UpdateConfiguration(Configuration{Address: addresses}))
			var timeout net.Error
			require.ErrorAs(t, read(p4, c4), &timeout)
			require.True(t, timeout.Timeout())
			require.NoError(t, d.UpdateConfiguration(Configuration{Address: []netip.Prefix{netip.MustParsePrefix("192.0.2.3/32"), addresses[1]}}))
			require.ErrorIs(t, read(p4, c4), net.ErrClosed)
			require.ErrorAs(t, read(p6, c6), &timeout)
			require.True(t, timeout.Timeout())
			require.NoError(t, d.UpdateConfiguration(Configuration{Address: []netip.Prefix{netip.MustParsePrefix("192.0.2.3/32")}}))
			require.ErrorIs(t, read(p6, c6), net.ErrClosed)
		})
	}
}
