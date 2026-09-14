package codexfp

import (
	"testing"

	utls "github.com/refraction-networking/utls"
)

func extIDs(exts []utls.TLSExtension) map[uint16]bool {
	// Build via a throwaway ClientHelloSpec marshal is overkill; instead assert on
	// the known extension set by type.
	seen := map[uint16]bool{}
	for _, e := range exts {
		switch e.(type) {
		case *utls.SignatureAlgorithmsExtension:
			seen[13] = true
		case *utls.SessionTicketExtension:
			seen[35] = true
		case *utls.SNIExtension:
			seen[0] = true
		case *utls.SupportedCurvesExtension:
			seen[10] = true
		case *utls.SupportedPointsExtension:
			seen[11] = true
		case *utls.KeyShareExtension:
			seen[51] = true
		case *utls.ExtendedMasterSecretExtension:
			seen[23] = true
		case *utls.SupportedVersionsExtension:
			seen[43] = true
		case *utls.StatusRequestExtension:
			seen[5] = true
		case *utls.PSKKeyExchangeModesExtension:
			seen[45] = true
		case *utls.ALPNExtension:
			seen[16] = true
		}
	}
	return seen
}

func TestClientHelloSpecShape(t *testing.T) {
	spec := ClientHelloSpec(ALPNHTTP)
	if len(spec.CipherSuites) != len(clientHelloCipherSuites) {
		t.Fatalf("cipher count=%d want=%d", len(spec.CipherSuites), len(clientHelloCipherSuites))
	}
	if spec.CipherSuites[0] != utls.TLS_AES_256_GCM_SHA384 {
		t.Fatalf("first cipher must be TLS_AES_256_GCM_SHA384, got %x", spec.CipherSuites[0])
	}
	seen := extIDs(spec.Extensions)
	// SNI(0) + ALPN(16) + 9 others = 11 extensions, matching real Codex JA4 t13d1011.
	for _, want := range []uint16{0, 5, 10, 11, 13, 16, 23, 35, 43, 45, 51} {
		if !seen[want] {
			t.Errorf("missing extension %d", want)
		}
	}
	if got := len(spec.Extensions); got != 11 {
		t.Errorf("extension count=%d want 11", got)
	}
}

func idOf(e utls.TLSExtension) uint16 {
	for id := range extIDs([]utls.TLSExtension{e}) {
		return id
	}
	return 0xffff
}

func orderOf(exts []utls.TLSExtension) []uint16 {
	ids := make([]uint16, len(exts))
	for i, e := range exts {
		ids[i] = idOf(e)
	}
	return ids
}

func TestClientHelloSpecShuffles(t *testing.T) {
	// rustls shuffles extension order per connection; across several specs the
	// order should vary while the set stays fixed.
	base := orderOf(ClientHelloSpec(ALPNHTTP).Extensions)
	differs := false
	for i := 0; i < 12 && !differs; i++ {
		next := orderOf(ClientHelloSpec(ALPNHTTP).Extensions)
		for j := range base {
			if base[j] != next[j] {
				differs = true
				break
			}
		}
	}
	if !differs {
		t.Error("expected shuffled extension order across specs")
	}
}

func TestPseudoHeaderOrder(t *testing.T) {
	want := []string{":method", ":scheme", ":authority", ":path"}
	if len(PseudoHeaderOrder) != len(want) {
		t.Fatalf("pseudo order len=%d", len(PseudoHeaderOrder))
	}
	for i := range want {
		if PseudoHeaderOrder[i] != want[i] {
			t.Fatalf("pseudo order[%d]=%s want %s", i, PseudoHeaderOrder[i], want[i])
		}
	}
}
