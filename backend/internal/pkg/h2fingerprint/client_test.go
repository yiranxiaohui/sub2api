package h2fingerprint

import (
	"crypto/tls"
	"strings"
	"testing"
	"time"

	utls "github.com/refraction-networking/utls"

	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
)

func TestNewClient_DefaultsBuild(t *testing.T) {
	c, err := NewClient(Options{})
	if err != nil {
		t.Fatalf("NewClient(zero) returned error: %v", err)
	}
	if c == nil {
		t.Fatal("NewClient returned nil client")
	}
}

func TestNewClient_RejectsALPNWithoutH2(t *testing.T) {
	_, err := NewClient(Options{
		TLSProfile: &tlsfingerprint.Profile{
			ALPNProtocols: []string{"http/1.1"},
		},
	})
	if err == nil {
		t.Fatal("expected NewClient to reject ALPN without h2")
	}
	if !strings.Contains(err.Error(), "h2") {
		t.Errorf("error should mention h2; got: %v", err)
	}
}

func TestNewClient_AcceptsALPNWithH2(t *testing.T) {
	_, err := NewClient(Options{
		TLSProfile: &tlsfingerprint.Profile{
			ALPNProtocols: []string{"h2", "http/1.1"},
		},
	})
	if err != nil {
		t.Fatalf("NewClient with h2 ALPN should succeed: %v", err)
	}
}

func TestResolveALPN_FillsEmpty(t *testing.T) {
	p, err := resolveALPN(nil)
	if err != nil {
		t.Fatalf("resolveALPN(nil): %v", err)
	}
	if len(p.ALPNProtocols) != 2 || p.ALPNProtocols[0] != "h2" || p.ALPNProtocols[1] != "http/1.1" {
		t.Errorf("ALPN not filled correctly: got %v", p.ALPNProtocols)
	}

	// Empty slice should be treated like nil.
	p, err = resolveALPN(&tlsfingerprint.Profile{ALPNProtocols: nil})
	if err != nil {
		t.Fatalf("resolveALPN(empty): %v", err)
	}
	if len(p.ALPNProtocols) == 0 {
		t.Error("empty ALPN was not filled")
	}
}

func TestResolveALPN_PreservesCallerOrder(t *testing.T) {
	// If the caller pinned ALPN with h2 already present, we should not
	// rewrite the order (it's part of the fingerprint).
	p, err := resolveALPN(&tlsfingerprint.Profile{
		ALPNProtocols: []string{"h2"},
	})
	if err != nil {
		t.Fatalf("resolveALPN: %v", err)
	}
	if len(p.ALPNProtocols) != 1 || p.ALPNProtocols[0] != "h2" {
		t.Errorf("h2-only ALPN was mutated: got %v", p.ALPNProtocols)
	}
}

func TestResolveALPN_DoesNotMutateCallerProfile(t *testing.T) {
	original := &tlsfingerprint.Profile{
		Name:          "caller",
		ALPNProtocols: nil,
	}
	_, err := resolveALPN(original)
	if err != nil {
		t.Fatalf("resolveALPN: %v", err)
	}
	if original.ALPNProtocols != nil {
		t.Errorf("caller's profile.ALPNProtocols was mutated: %v", original.ALPNProtocols)
	}
}

func TestBuildTLSHandshakeFunc_ReturnsCallable(t *testing.T) {
	// We can't easily drive a full handshake in a unit test (would need a
	// real TLS server with a matching cert), but we can at least confirm
	// the constructed function is non-nil and has the right shape.
	fn := buildTLSHandshakeFunc(nil)
	if fn == nil {
		t.Fatal("buildTLSHandshakeFunc returned nil")
	}
}

// TestConvertConnectionState_CopiesEssentialFields verifies that the fields
// req relies on (NegotiatedProtocol especially — that's what selects h2 vs h1)
// survive the utls→stdlib translation.
func TestConvertConnectionState_CopiesEssentialFields(t *testing.T) {
	// We can't construct a non-zero utls.ConnectionState without doing a real
	// handshake, but we can at least verify the zero-value translation
	// doesn't panic and yields a zero-value tls.ConnectionState.
	in := utlsConnStateForTest()
	got := convertConnectionState(in)

	if got.NegotiatedProtocol != "h2" {
		t.Errorf("NegotiatedProtocol: got %q, want %q", got.NegotiatedProtocol, "h2")
	}
	if got.ServerName != "api.anthropic.com" {
		t.Errorf("ServerName: got %q", got.ServerName)
	}
	if got.Version != tls.VersionTLS13 {
		t.Errorf("Version: got %d, want %d", got.Version, tls.VersionTLS13)
	}
	if !got.HandshakeComplete {
		t.Error("HandshakeComplete not propagated")
	}
}

// TestNewClient_WithTimeout confirms the timeout knob plumbs through.
func TestNewClient_WithTimeout(t *testing.T) {
	c, err := NewClient(Options{Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	// Use the public req.Client API to verify the timeout reached the underlying *http.Client.
	if c.GetClient() == nil {
		t.Fatal("GetClient() returned nil")
	}
	if c.GetClient().Timeout != 5*time.Second {
		t.Errorf("timeout not propagated: got %v", c.GetClient().Timeout)
	}
}

// TestNewClient_RequestBuilds confirms the client can construct a request
// without panic — a regression check that the chain of req.C().Enable... /
// Set... calls is internally consistent.
func TestNewClient_RequestBuilds(t *testing.T) {
	c, err := NewClient(Options{})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	r := c.R().
		SetHeader("User-Agent", "claude-cli/2.1.81").
		SetHeader("anthropic-beta", "oauth-2025-04-20")
	if r == nil {
		t.Fatal("R() returned nil")
	}
}

// ---- helpers ----

// utlsConnStateForTest fabricates a utls.ConnectionState with the fields
// convertConnectionState should preserve. Real handshakes set these too —
// this is the unit-test substitute.
func utlsConnStateForTest() utls.ConnectionState {
	return utls.ConnectionState{
		Version:            tls.VersionTLS13,
		HandshakeComplete:  true,
		CipherSuite:        tls.TLS_AES_128_GCM_SHA256,
		NegotiatedProtocol: "h2",
		ServerName:         "api.anthropic.com",
	}
}
