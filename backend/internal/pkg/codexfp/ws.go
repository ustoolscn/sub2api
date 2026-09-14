package codexfp

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"golang.org/x/net/proxy"
)

func wsInsecureSkipVerify() bool {
	return os.Getenv("CODEXFP_INSECURE_SKIP_VERIFY") == "1"
}

// dialTLSContext returns an http.Transport.DialTLSContext that performs a
// rustls-shaped TLS handshake (ALPN http/1.1) after connecting directly or
// tunneling through an HTTP(S)/SOCKS5 proxy. WebSocket upgrades run over
// HTTP/1.1, so only http/1.1 is offered in ALPN.
func dialTLSContext(proxyURL *url.URL, insecure bool) func(ctx context.Context, network, addr string) (net.Conn, error) {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		conn, err := dialRaw(ctx, proxyURL, addr)
		if err != nil {
			return nil, err
		}
		uconn, err := Handshake(ctx, conn, addr, HandshakeOptions{ALPN: ALPNHTTP1, InsecureSkipVerify: insecure})
		if err != nil {
			return nil, err
		}
		return uconn, nil
	}
}

func dialRaw(ctx context.Context, proxyURL *url.URL, addr string) (net.Conn, error) {
	if proxyURL == nil {
		return (&net.Dialer{}).DialContext(ctx, "tcp", addr)
	}
	switch strings.ToLower(proxyURL.Scheme) {
	case "socks5", "socks5h":
		return dialSOCKS5(proxyURL, addr)
	case "http", "https":
		return dialHTTPConnect(ctx, proxyURL, addr)
	default:
		return (&net.Dialer{}).DialContext(ctx, "tcp", addr)
	}
}

func dialSOCKS5(proxyURL *url.URL, addr string) (net.Conn, error) {
	var auth *proxy.Auth
	if proxyURL.User != nil {
		user := proxyURL.User.Username()
		pass, _ := proxyURL.User.Password()
		auth = &proxy.Auth{User: user, Password: pass}
	}
	proxyAddr := proxyURL.Host
	if proxyURL.Port() == "" {
		proxyAddr = net.JoinHostPort(proxyURL.Hostname(), "1080")
	}
	d, err := proxy.SOCKS5("tcp", proxyAddr, auth, proxy.Direct)
	if err != nil {
		return nil, fmt.Errorf("codexfp: socks5 dialer: %w", err)
	}
	return d.Dial("tcp", addr)
}

func dialHTTPConnect(ctx context.Context, proxyURL *url.URL, addr string) (net.Conn, error) {
	proxyAddr := proxyURL.Host
	if proxyURL.Port() == "" {
		if strings.EqualFold(proxyURL.Scheme, "https") {
			proxyAddr = net.JoinHostPort(proxyURL.Hostname(), "443")
		} else {
			proxyAddr = net.JoinHostPort(proxyURL.Hostname(), "80")
		}
	}
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", proxyAddr)
	if err != nil {
		return nil, fmt.Errorf("codexfp: connect proxy: %w", err)
	}
	req := &http.Request{
		Method: http.MethodConnect,
		URL:    &url.URL{Opaque: addr},
		Host:   addr,
		Header: make(http.Header),
	}
	if proxyURL.User != nil {
		user := proxyURL.User.Username()
		pass, _ := proxyURL.User.Password()
		token := base64.StdEncoding.EncodeToString([]byte(user + ":" + pass))
		req.Header.Set("Proxy-Authorization", "Basic "+token)
	}
	if err := req.Write(conn); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("codexfp: write CONNECT: %w", err)
	}
	resp, err := http.ReadResponse(bufio.NewReader(conn), req)
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("codexfp: read CONNECT: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		_ = conn.Close()
		return nil, fmt.Errorf("codexfp: proxy CONNECT failed: %s", resp.Status)
	}
	return conn, nil
}

// NewWebSocketHTTPClient builds an *http.Client suitable for coder/websocket
// whose TLS ClientHello matches the official Codex CLI. HTTP/2 is disabled
// because WebSocket upgrades run over HTTP/1.1.
func NewWebSocketHTTPClient(proxyURLStr string, timeout time.Duration) (*http.Client, error) {
	var proxyURL *url.URL
	if p := strings.TrimSpace(proxyURLStr); p != "" {
		parsed, err := url.Parse(p)
		if err != nil {
			return nil, err
		}
		proxyURL = parsed
	}
	transport := &http.Transport{
		ForceAttemptHTTP2:   false,
		TLSNextProto:        make(map[string]func(string, *tls.Conn) http.RoundTripper),
		DialTLSContext:      dialTLSContext(proxyURL, wsInsecureSkipVerify()),
		DisableCompression:  true, // real Codex sends no accept-encoding
		MaxIdleConns:        100,
		IdleConnTimeout:     90 * time.Second,
		TLSHandshakeTimeout: 10 * time.Second,
	}
	return &http.Client{Transport: transport, Timeout: timeout}, nil
}
