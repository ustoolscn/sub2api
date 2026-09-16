package codexfp

import (
	"bufio"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// newH2TestServer returns a TLS server that offers h2 via ALPN, like chatgpt.com.
func newH2TestServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Proto", r.Proto)
		_, _ = io.WriteString(w, "ok")
	}))
	srv.EnableHTTP2 = true
	srv.StartTLS()
	t.Cleanup(srv.Close)
	return srv
}

func doGet(t *testing.T, rt http.RoundTripper, url string) *http.Response {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatalf("round trip: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "ok" {
		t.Fatalf("body=%q", body)
	}
	return resp
}

// TestNewTransportNegotiatesH2 reproduces the production failure
// `http2: could not negotiate protocol mutually`: the uTLS connection state
// must be reported as a mutual ALPN negotiation or req refuses the h2 conn.
func TestNewTransportNegotiatesH2(t *testing.T) {
	srv := newH2TestServer(t)
	rt, err := NewTransport(TransportOptions{InsecureSkipVerify: true})
	if err != nil {
		t.Fatal(err)
	}
	resp := doGet(t, rt, srv.URL)
	if resp.ProtoMajor != 2 {
		t.Fatalf("proto=%s want HTTP/2.0", resp.Proto)
	}
	if got := resp.Header.Get("X-Proto"); got != "HTTP/2.0" {
		t.Fatalf("server saw %s want HTTP/2.0", got)
	}
}

// startConnectProxy runs a minimal HTTP CONNECT proxy and counts tunnels.
func startConnectProxy(t *testing.T) (addr string, tunnels *atomic.Int32) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	tunnels = &atomic.Int32{}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer func() { _ = c.Close() }()
				br := bufio.NewReader(c)
				req, err := http.ReadRequest(br)
				if err != nil || req.Method != http.MethodConnect {
					return
				}
				up, err := net.Dial("tcp", req.Host)
				if err != nil {
					_, _ = io.WriteString(c, "HTTP/1.1 502 Bad Gateway\r\n\r\n")
					return
				}
				defer func() { _ = up.Close() }()
				tunnels.Add(1)
				if _, err := io.WriteString(c, "HTTP/1.1 200 Connection established\r\n\r\n"); err != nil {
					return
				}
				done := make(chan struct{}, 2)
				go func() { _, _ = io.Copy(up, br); done <- struct{}{} }()
				go func() { _, _ = io.Copy(c, up); done <- struct{}{} }()
				<-done
			}(c)
		}
	}()
	return ln.Addr().String(), tunnels
}

// TestNewTransportHonorsProxy guards against the fingerprint transport
// bypassing the account proxy: req's forced-HTTP/2 mode dials the origin
// directly, so the tunnel must be observed on the proxy.
func TestNewTransportHonorsProxy(t *testing.T) {
	srv := newH2TestServer(t)
	proxyAddr, tunnels := startConnectProxy(t)
	rt, err := NewTransport(TransportOptions{ProxyURL: "http://" + proxyAddr, InsecureSkipVerify: true})
	if err != nil {
		t.Fatal(err)
	}
	resp := doGet(t, rt, srv.URL)
	if resp.ProtoMajor != 2 {
		t.Fatalf("proto=%s want HTTP/2.0", resp.Proto)
	}
	if n := tunnels.Load(); n != 1 {
		t.Fatalf("proxy tunnels=%d want 1 (request bypassed the proxy)", n)
	}
}

func TestStdConnectionStateALPNIsMutual(t *testing.T) {
	srv := newH2TestServer(t)
	raw, err := net.Dial("tcp", strings.TrimPrefix(srv.URL, "https://"))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	uconn, err := Handshake(ctx, raw, "127.0.0.1", HandshakeOptions{ALPN: ALPNHTTP, InsecureSkipVerify: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = uconn.Close() }()
	st := StdConnectionState(uconn)
	if st.NegotiatedProtocol != "h2" {
		t.Fatalf("negotiated=%q want h2", st.NegotiatedProtocol)
	}
	if !st.NegotiatedProtocolIsMutual { //nolint:staticcheck // req reads it
		t.Fatal("NegotiatedProtocolIsMutual must be true for req's http2 dialer")
	}
}
