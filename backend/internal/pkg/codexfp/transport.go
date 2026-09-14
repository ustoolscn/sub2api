package codexfp

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"net/url"
	"os"
	"time"

	reqhttp2 "github.com/imroc/req/v3/http2"

	"github.com/imroc/req/v3"
	utls "github.com/refraction-networking/utls"
)

// reqTLSConn adapts a *utls.UConn to the reqtls.Conn interface req expects:
// net.Conn + Handshake/HandshakeContext (inherited) plus a ConnectionState that
// returns the standard crypto/tls type req uses for ALPN checks.
type reqTLSConn struct {
	*utls.UConn
}

func (c reqTLSConn) ConnectionState() tls.ConnectionState {
	return *StdConnectionState(c.UConn)
}

// HTTP/2 connection preface captured from codex-cli 0.154.0 (reqwest + hyper).
// The SETTINGS frame order and values, plus the connection-level WINDOW_UPDATE,
// are part of the fingerprint and must be reproduced exactly.
var (
	h2Settings = []reqhttp2.Setting{
		{ID: reqhttp2.SettingEnablePush, Val: 0},
		{ID: reqhttp2.SettingInitialWindowSize, Val: 2097152},
		{ID: reqhttp2.SettingMaxFrameSize, Val: 16384},
		{ID: reqhttp2.SettingMaxHeaderListSize, Val: 16384},
	}
	h2ConnectionFlow uint32 = 5177345
)

// PseudoHeaderOrder is the HTTP/2 pseudo-header order hyper emits.
var PseudoHeaderOrder = []string{":method", ":scheme", ":authority", ":path"}

// TransportOptions configures a Codex-shaped round tripper.
type TransportOptions struct {
	ProxyURL              string
	MaxIdleConns          int
	MaxIdleConnsPerHost   int
	MaxConnsPerHost       int
	IdleConnTimeout       time.Duration
	TLSHandshakeTimeout   time.Duration
	ResponseHeaderTimeout time.Duration
	ReadIdleTimeout       time.Duration
	PingTimeout           time.Duration
	// InsecureSkipVerify is only for local capture/verification tooling.
	InsecureSkipVerify bool
}

// NewTransport builds an http.RoundTripper whose TLS ClientHello and HTTP/2
// preface match the official Codex CLI. It negotiates h2 via ALPN and falls
// back to http/1.1 only if the server does. Auto gzip is disabled because the
// real client advertises no accept-encoding.
func NewTransport(opts TransportOptions) (http.RoundTripper, error) {
	t := req.NewTransport()
	t.SetHTTP2SettingsFrame(h2Settings...)
	t.SetHTTP2ConnectionFlow(h2ConnectionFlow)
	t.EnableForceHTTP2()
	t.DisableCompression = true

	if opts.MaxIdleConns > 0 {
		t.SetMaxIdleConns(opts.MaxIdleConns)
	}
	if opts.MaxIdleConnsPerHost > 0 {
		t.MaxIdleConnsPerHost = opts.MaxIdleConnsPerHost
	}
	if opts.MaxConnsPerHost > 0 {
		t.SetMaxConnsPerHost(opts.MaxConnsPerHost)
	}
	if opts.IdleConnTimeout > 0 {
		t.SetIdleConnTimeout(opts.IdleConnTimeout)
	}
	if opts.TLSHandshakeTimeout > 0 {
		t.SetTLSHandshakeTimeout(opts.TLSHandshakeTimeout)
	}
	if opts.ResponseHeaderTimeout > 0 {
		t.SetResponseHeaderTimeout(opts.ResponseHeaderTimeout)
	}
	if opts.ReadIdleTimeout > 0 {
		t.SetHTTP2ReadIdleTimeout(opts.ReadIdleTimeout)
	}
	if opts.PingTimeout > 0 {
		t.SetHTTP2PingTimeout(opts.PingTimeout)
	}

	if p := opts.ProxyURL; p != "" {
		parsed, err := url.Parse(p)
		if err != nil {
			return nil, err
		}
		t.SetProxy(http.ProxyURL(parsed))
	} else {
		t.SetProxy(nil)
	}

	// CODEXFP_INSECURE_SKIP_VERIFY is a capture/verification escape hatch only.
	insecure := opts.InsecureSkipVerify || os.Getenv("CODEXFP_INSECURE_SKIP_VERIFY") == "1"
	t.SetTLSHandshake(func(ctx context.Context, addr string, plainConn net.Conn) (net.Conn, *tls.ConnectionState, error) {
		uconn, err := Handshake(ctx, plainConn, addr, HandshakeOptions{ALPN: ALPNHTTP, InsecureSkipVerify: insecure})
		if err != nil {
			return nil, nil, err
		}
		return reqTLSConn{uconn}, StdConnectionState(uconn), nil
	})

	return t, nil
}

// ApplyHeaderOrder stamps the Codex header ordering magic keys onto req so the
// HTTP/2 HEADERS frame lists fields in the captured order. Unlisted headers keep
// their insertion order after the listed ones.
func ApplyHeaderOrder(header http.Header, order []string) {
	if header == nil {
		return
	}
	header[req.PseudoHeaderOderKey] = append([]string(nil), PseudoHeaderOrder...)
	if len(order) > 0 {
		header[req.HeaderOderKey] = append([]string(nil), order...)
	}
}
