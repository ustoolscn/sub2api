// Package codexfp reproduces the network fingerprint of the official Codex CLI
// (codex-rs: reqwest + hyper + rustls) for outbound OpenAI/ChatGPT traffic, so
// gateway requests are not distinguishable from the real client by TLS
// ClientHello, HTTP/2 connection preface, or header layout.
//
// All constants were captured from codex-cli 0.154.0 (both API-key and ChatGPT
// OAuth modes) against a local capture server; see codexfp_test.go.
package codexfp

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"sync/atomic"

	utls "github.com/refraction-networking/utls"
)

// enabled mirrors gateway.openai_codex_fingerprint for outbound paths that
// cannot see the config directly (the shared WebSocket dialer). Default true so
// zero-value construction (tests/tools) keeps the disguise on.
var enabled = func() *atomic.Bool {
	v := &atomic.Bool{}
	v.Store(true)
	return v
}()

// SetEnabled publishes the process-wide Codex fingerprint switch.
func SetEnabled(on bool) { enabled.Store(on) }

// Enabled reports whether the Codex network fingerprint is active.
func Enabled() bool { return enabled.Load() }

// FingerprintHosts are the real ChatGPT/OpenAI Codex hostnames the disguise
// applies to. Shared by the HTTP and WebSocket paths.
var FingerprintHosts = map[string]bool{
	"chatgpt.com":     true,
	"api.openai.com":  true,
	"auth.openai.com": true,
}

// IsFingerprintHost reports whether host is a real Codex endpoint.
func IsFingerprintHost(host string) bool {
	return FingerprintHosts[host]
}

// rustls default cipher preference: TLS 1.3 suites first, ECDHE AEADs only,
// plus the renegotiation SCSV.
var clientHelloCipherSuites = []uint16{
	utls.TLS_AES_256_GCM_SHA384,
	utls.TLS_AES_128_GCM_SHA256,
	utls.TLS_CHACHA20_POLY1305_SHA256,
	utls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
	utls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
	utls.TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305_SHA256,
	utls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
	utls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
	utls.TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305_SHA256,
	utls.FAKE_TLS_EMPTY_RENEGOTIATION_INFO_SCSV,
}

var clientHelloSignatureAlgorithms = []utls.SignatureScheme{
	utls.ECDSAWithP384AndSHA384,
	utls.ECDSAWithP256AndSHA256,
	utls.ECDSAWithP521AndSHA512,
	utls.Ed25519,
	utls.PSSWithSHA512,
	utls.PSSWithSHA384,
	utls.PSSWithSHA256,
	utls.PKCS1WithSHA512,
	utls.PKCS1WithSHA384,
	utls.PKCS1WithSHA256,
}

// ALPN offers used by the two Codex transports.
var (
	ALPNHTTP  = []string{"h2", "http/1.1"}
	ALPNHTTP1 = []string{"http/1.1"}
)

// ClientHelloSpec returns a fresh rustls-shaped ClientHello. rustls shuffles
// its extension order per connection (JA3 varies, JA4 is stable), so every
// call yields a newly shuffled order.
func ClientHelloSpec(alpn []string) *utls.ClientHelloSpec {
	extensions := []utls.TLSExtension{
		&utls.SignatureAlgorithmsExtension{SupportedSignatureAlgorithms: clientHelloSignatureAlgorithms},
		&utls.SessionTicketExtension{},
		&utls.SNIExtension{},
		&utls.SupportedCurvesExtension{Curves: []utls.CurveID{utls.X25519MLKEM768, utls.X25519, utls.CurveP256, utls.CurveP384}},
		&utls.SupportedPointsExtension{SupportedPoints: []byte{0}},
		&utls.KeyShareExtension{KeyShares: []utls.KeyShare{{Group: utls.X25519MLKEM768}, {Group: utls.X25519}}},
		&utls.ExtendedMasterSecretExtension{},
		&utls.SupportedVersionsExtension{Versions: []uint16{utls.VersionTLS13, utls.VersionTLS12}},
		&utls.StatusRequestExtension{},
		&utls.PSKKeyExchangeModesExtension{Modes: []uint8{utls.PskModeDHE}},
	}
	if len(alpn) > 0 {
		extensions = append(extensions, &utls.ALPNExtension{AlpnProtocols: append([]string(nil), alpn...)})
	}
	return &utls.ClientHelloSpec{
		CipherSuites:       append([]uint16(nil), clientHelloCipherSuites...),
		CompressionMethods: []byte{0},
		Extensions:         utls.ShuffleChromeTLSExtensions(extensions),
		TLSVersMin:         utls.VersionTLS12,
		TLSVersMax:         utls.VersionTLS13,
	}
}

// HandshakeOptions tunes a Codex-shaped TLS handshake.
type HandshakeOptions struct {
	ALPN []string
	// InsecureSkipVerify is only for local capture/verification tooling.
	InsecureSkipVerify bool
}

// Handshake performs a rustls-shaped TLS handshake on an established
// connection. addr may be "host" or "host:port". On failure conn is closed.
func Handshake(ctx context.Context, conn net.Conn, addr string, opts HandshakeOptions) (*utls.UConn, error) {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	uconn := utls.UClient(conn, &utls.Config{
		ServerName:         host,
		InsecureSkipVerify: opts.InsecureSkipVerify, //nolint:gosec // capture tooling only
	}, utls.HelloCustom)
	if err := uconn.ApplyPreset(ClientHelloSpec(opts.ALPN)); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("codexfp: apply client hello: %w", err)
	}
	// rustls does not send a legacy (middlebox compatibility) session id.
	uconn.HandshakeState.Hello.SessionId = []byte{}
	if err := uconn.HandshakeContext(ctx); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("codexfp: tls handshake: %w", err)
	}
	return uconn, nil
}

// StdConnectionState converts the uTLS state into the crypto/tls shape that
// net/http-style transports use for ALPN protocol selection.
func StdConnectionState(uconn *utls.UConn) *tls.ConnectionState {
	st := uconn.ConnectionState()
	return &tls.ConnectionState{
		Version:            st.Version,
		HandshakeComplete:  st.HandshakeComplete,
		DidResume:          st.DidResume,
		CipherSuite:        st.CipherSuite,
		NegotiatedProtocol: st.NegotiatedProtocol,
		ServerName:         st.ServerName,
		PeerCertificates:   st.PeerCertificates,
		VerifiedChains:     st.VerifiedChains,
		OCSPResponse:       st.OCSPResponse,
	}
}
