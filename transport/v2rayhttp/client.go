package v2rayhttp

import (
	"context"
	"io"
	"math/rand"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/badhttp"
	"github.com/sagernet/sing-box/common/tls"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common"
	E "github.com/sagernet/sing/common/exceptions"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"

	"golang.org/x/net/http/httpguts"
	"golang.org/x/net/http2"
)

var _ adapter.V2RayMultiplexClientTransport = (*Client)(nil)

type Client struct {
	ctx        context.Context
	dialer     N.Dialer
	serverAddr M.Socksaddr
	transport  common.TypedValue[http.RoundTripper]
	http2      bool
	rawTCP     bool
	requestURL url.URL
	host       []string
	method     string
	headers    http.Header
	closeIdle  atomic.Bool
}

func NewClient(ctx context.Context, dialer N.Dialer, serverAddr M.Socksaddr, options option.V2RayHTTPOptions, tlsConfig tls.Config) (adapter.V2RayClientTransport, error) {
	var transport http.RoundTripper
	if options.RawTCP {
		if tlsConfig != nil {
			dialer = tls.NewDialer(dialer, tlsConfig)
		}
	} else if tlsConfig == nil {
		transport = &http.Transport{
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				return dialer.DialContext(ctx, network, M.ParseSocksaddr(addr))
			},
		}
	} else {
		if len(tlsConfig.NextProtos()) == 0 {
			tlsConfig.SetNextProtos([]string{http2.NextProtoTLS})
		}
		tlsDialer := tls.NewDialer(dialer, tlsConfig)
		transport = &http2.Transport{
			ReadIdleTimeout: time.Duration(options.IdleTimeout),
			PingTimeout:     time.Duration(options.PingTimeout),
			DialTLSContext: func(ctx context.Context, network, addr string, cfg *tls.STDConfig) (net.Conn, error) {
				return tlsDialer.DialTLSContext(ctx, M.ParseSocksaddr(addr))
			},
		}
	}
	if options.Method == "" {
		if options.RawTCP {
			options.Method = http.MethodGet
		} else {
			options.Method = http.MethodPut
		}
	}
	headers := options.Headers.Build()
	if options.RawTCP {
		if !httpguts.ValidHeaderFieldName(options.Method) {
			return nil, E.New("invalid TCP HTTP method")
		}
		for _, host := range options.Host {
			if !httpguts.ValidHostHeader(host) {
				return nil, E.New("invalid TCP HTTP host")
			}
		}
		for name, values := range headers {
			if !httpguts.ValidHeaderFieldName(name) {
				return nil, E.New("invalid TCP HTTP header name")
			}
			for _, value := range values {
				if !httpguts.ValidHeaderFieldValue(value) {
					return nil, E.New("invalid TCP HTTP header value")
				}
			}
		}
	}
	var requestURL url.URL
	if tlsConfig == nil {
		requestURL.Scheme = "http"
	} else {
		requestURL.Scheme = "https"
	}
	requestURL.Host = serverAddr.String()
	if options.RawTCP {
		path := options.Path
		if !strings.HasPrefix(path, "/") {
			path = "/" + path
		}
		target, err := url.ParseRequestURI(path)
		if err != nil {
			return nil, E.Cause(err, "parse TCP request target")
		}
		requestURL.Path, requestURL.RawPath = target.Path, target.RawPath
		requestURL.RawQuery, requestURL.ForceQuery = target.RawQuery, target.ForceQuery
	} else {
		requestURL.Path = options.Path
		if err := badhttp.URLSetPath(&requestURL, options.Path); err != nil {
			return nil, E.Cause(err, "parse path")
		}
		if !strings.HasPrefix(requestURL.Path, "/") {
			requestURL.Path = "/" + requestURL.Path
		}
	}
	client := &Client{
		ctx:        ctx,
		dialer:     dialer,
		serverAddr: serverAddr,
		requestURL: requestURL,
		host:       options.Host,
		method:     options.Method,
		headers:    headers,
		http2:      tlsConfig != nil && !options.RawTCP,
		rawTCP:     options.RawTCP,
	}
	client.transport.Store(transport)
	return client, nil
}

func (c *Client) DialContext(ctx context.Context) (net.Conn, error) {
	if !c.http2 {
		return c.dialHTTP(ctx)
	} else {
		return c.dialHTTP2(ctx)
	}
}

func (c *Client) dialHTTP(ctx context.Context) (net.Conn, error) {
	conn, err := c.dialer.DialContext(ctx, N.NetworkTCP, c.serverAddr)
	if err != nil {
		return nil, err
	}

	request := &http.Request{
		Method: c.method,
		URL:    &c.requestURL,
		Header: c.headers.Clone(),
	}
	switch hostLen := len(c.host); hostLen {
	case 0:
		request.Host = c.serverAddr.AddrString()
	case 1:
		request.Host = c.host[0]
	default:
		request.Host = c.host[rand.Intn(hostLen)]
	}
	if c.rawTCP {
		for key, values := range request.Header {
			if len(values) > 1 {
				request.Header[key] = []string{values[rand.Intn(len(values))]}
			}
		}
	}
	httpConn := NewHTTP1Conn(conn, request)
	httpConn.rawTCP = c.rawTCP
	return httpConn, nil
}

func (c *Client) dialHTTP2(ctx context.Context) (net.Conn, error) {
	pipeInReader, pipeInWriter := io.Pipe()
	requestCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	request := &http.Request{
		Method: c.method,
		Body:   pipeInReader,
		URL:    &c.requestURL,
		Header: c.headers.Clone(),
	}
	request = request.WithContext(requestCtx)
	switch hostLen := len(c.host); hostLen {
	case 0:
		// https://github.com/v2fly/v2ray-core/blob/master/transport/internet/http/config.go#L13
		request.Host = "www.example.com"
	case 1:
		request.Host = c.host[0]
	default:
		request.Host = c.host[rand.Intn(hostLen)]
	}
	conn := NewLateHTTPConn(pipeInWriter, cancel)
	keepSession := adapter.KeepSessionFromContext(ctx)
	conn.onClose = func() {
		if c.closeIdle.Load() && !keepSession {
			CloseIdleConnections(c.transport.Load())
		}
	}
	handshakeTimeout := C.TCPTimeout
	if deadline, hasDeadline := ctx.Deadline(); hasDeadline {
		handshakeTimeout = time.Until(deadline)
	}
	var handshakeTimedOut atomic.Bool
	handshakeTimer := time.AfterFunc(handshakeTimeout, func() {
		handshakeTimedOut.Store(true)
		cancel()
	})
	go func() {
		response, err := c.transport.Load().RoundTrip(request)
		handshakeTimer.Stop()
		if err != nil {
			if handshakeTimedOut.Load() {
				err = os.ErrDeadlineExceeded
			}
			conn.Setup(nil, err)
		} else if response.StatusCode != 200 {
			response.Body.Close()
			conn.Setup(nil, E.New("v2ray-http: unexpected status: ", response.Status))
		} else {
			conn.Setup(response.Body, nil)
		}
	}()
	return conn, nil
}

func (c *Client) MultiplexEnabled() bool {
	return c.http2
}

func (c *Client) SetKeepIdleConnections(keep bool) {
	c.closeIdle.Store(!keep)
	if !keep {
		c.CloseIdleConnections()
	}
}

func (c *Client) CloseIdleConnections() {
	if !c.rawTCP {
		CloseIdleConnections(c.transport.Load())
	}
}

func (c *Client) Close() error {
	if !c.rawTCP {
		c.transport.Store(ResetTransport(c.transport.Load()))
	}
	return nil
}
