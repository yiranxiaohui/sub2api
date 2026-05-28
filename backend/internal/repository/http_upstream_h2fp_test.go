package repository

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

// stubUpstream records every call and returns a canned response so we can
// assert which path (h2fp vs base) the service used without booting real
// network listeners.
type stubUpstream struct {
	mu             sync.Mutex
	doCalls        int
	doWithTLSCalls int
	lastProfile    *tlsfingerprint.Profile
	returnErr      error
}

func (s *stubUpstream) Do(req *http.Request, proxyURL string, accountID int64, accountConcurrency int) (*http.Response, error) {
	s.mu.Lock()
	s.doCalls++
	s.mu.Unlock()
	if s.returnErr != nil {
		return nil, s.returnErr
	}
	return cannedResponse("base.Do"), nil
}

func (s *stubUpstream) DoWithTLS(req *http.Request, proxyURL string, accountID int64, accountConcurrency int, profile *tlsfingerprint.Profile) (*http.Response, error) {
	s.mu.Lock()
	s.doWithTLSCalls++
	s.lastProfile = profile
	s.mu.Unlock()
	if s.returnErr != nil {
		return nil, s.returnErr
	}
	return cannedResponse("base.DoWithTLS"), nil
}

func (s *stubUpstream) counts() (do, doTLS int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.doCalls, s.doWithTLSCalls
}

func cannedResponse(tag string) *http.Response {
	return &http.Response{
		StatusCode: 200,
		Body:       io.NopCloser(strings.NewReader(tag)),
		Header:     http.Header{},
	}
}

// newServiceForTest builds an h2fpUpstreamService with the supplied stub as
// the base and the supplied feature flag.
func newServiceForTest(stub service.HTTPUpstream, enabled bool, fallbackOnError bool) *h2fpUpstreamService {
	cfg := &config.Config{}
	cfg.Gateway.H2Fingerprint.Enabled = enabled
	cfg.Gateway.H2Fingerprint.FallbackOnError = fallbackOnError
	cfg.Gateway.H2Fingerprint.FallbackErrorThreshold = 2
	cfg.Gateway.H2Fingerprint.FallbackWindowSeconds = 60
	cfg.Gateway.H2Fingerprint.FallbackTTLSeconds = 60
	return &h2fpUpstreamService{cfg: cfg, base: stub}
}

func newTestRequest() *http.Request {
	req, _ := http.NewRequest("POST", "https://api.anthropic.com/v1/messages", strings.NewReader(`{}`))
	return req
}

func TestH2FP_FlagOff_DoWithTLS_Delegates(t *testing.T) {
	stub := &stubUpstream{}
	svc := newServiceForTest(stub, false, true)

	profile := &tlsfingerprint.Profile{Name: "Node.js"}
	_, err := svc.DoWithTLS(newTestRequest(), "", 1, 1, profile)
	if err != nil {
		t.Fatalf("DoWithTLS: %v", err)
	}

	do, doTLS := stub.counts()
	if doTLS != 1 || do != 0 {
		t.Errorf("flag-off should delegate exactly once via DoWithTLS: do=%d, doTLS=%d", do, doTLS)
	}
}

func TestH2FP_ProfileNil_AlwaysDelegates(t *testing.T) {
	stub := &stubUpstream{}
	// Even with flag ON, a nil profile means "no fingerprint requested" — must delegate.
	svc := newServiceForTest(stub, true, true)

	if _, err := svc.DoWithTLS(newTestRequest(), "", 1, 1, nil); err != nil {
		t.Fatalf("DoWithTLS: %v", err)
	}
	do, doTLS := stub.counts()
	// The base impl's contract: DoWithTLS(profile=nil) is equivalent to Do().
	// We honor that by passing the call straight through to base.DoWithTLS,
	// which itself short-circuits to Do internally.
	if doTLS != 1 || do != 0 {
		t.Errorf("nil-profile path should delegate to base DoWithTLS once: do=%d, doTLS=%d", do, doTLS)
	}
}

func TestH2FP_Do_AlwaysDelegates(t *testing.T) {
	stub := &stubUpstream{}
	svc := newServiceForTest(stub, true, true)

	if _, err := svc.Do(newTestRequest(), "", 1, 1); err != nil {
		t.Fatalf("Do: %v", err)
	}
	do, doTLS := stub.counts()
	if do != 1 || doTLS != 0 {
		t.Errorf("Do should always delegate to base.Do: do=%d, doTLS=%d", do, doTLS)
	}
}

func TestH2FP_FlagOn_FailureWithFallback_RetriesBase(t *testing.T) {
	// We can't easily simulate a real h2fp client failure inline (would need
	// to inject the client), so instead we drive the service against a
	// definitely-unreachable proxy. NewClient succeeds but the actual request
	// will fail at dial time. fallback_on_error=true must then call base.
	stub := &stubUpstream{}
	svc := newServiceForTest(stub, true, true)

	profile := &tlsfingerprint.Profile{
		Name:          "Node.js",
		ALPNProtocols: []string{"h2", "http/1.1"},
	}
	req, _ := http.NewRequest("GET", "https://example.invalid/", nil)

	_, err := svc.DoWithTLS(req, "http://127.0.0.1:1/", 1, 1, profile)
	if err != nil {
		t.Fatalf("expected fallback to succeed, got: %v", err)
	}
	do, doTLS := stub.counts()
	if doTLS != 1 {
		t.Errorf("expected one fallback DoWithTLS call: doTLS=%d (do=%d)", doTLS, do)
	}
}

func TestH2FP_FlagOn_FailureWithoutFallback_ReturnsError(t *testing.T) {
	stub := &stubUpstream{}
	svc := newServiceForTest(stub, true, false /* fallback off */)

	profile := &tlsfingerprint.Profile{
		Name:          "Node.js",
		ALPNProtocols: []string{"h2", "http/1.1"},
	}
	req, _ := http.NewRequest("GET", "https://example.invalid/", nil)

	_, err := svc.DoWithTLS(req, "http://127.0.0.1:1/", 1, 1, profile)
	if err == nil {
		t.Fatal("expected error when fallback disabled")
	}
	do, doTLS := stub.counts()
	if doTLS != 0 || do != 0 {
		t.Errorf("base should not be called when fallback disabled: do=%d, doTLS=%d", do, doTLS)
	}
}

func TestH2FP_CircuitBreaker_TripsThenForcesFallback(t *testing.T) {
	stub := &stubUpstream{}
	svc := newServiceForTest(stub, true, true)
	// Tighten threshold so we can trip after 2 errors.
	svc.cfg.Gateway.H2Fingerprint.FallbackErrorThreshold = 2

	proxyKey := normalizeProxyKey("http://127.0.0.1:1/")

	// Manually record two errors to simulate two failed requests.
	svc.recordError(proxyKey)
	if svc.circuitOpen(proxyKey) {
		t.Error("circuit should not be open after a single error")
	}
	svc.recordError(proxyKey)
	if !svc.circuitOpen(proxyKey) {
		t.Fatal("circuit should be open after threshold errors")
	}

	// Now any request through this proxy must short-circuit to base without
	// even building an h2fp client.
	profile := &tlsfingerprint.Profile{Name: "Node.js", ALPNProtocols: []string{"h2", "http/1.1"}}
	req, _ := http.NewRequest("GET", "https://example.invalid/", nil)
	if _, err := svc.DoWithTLS(req, "http://127.0.0.1:1/", 1, 1, profile); err != nil {
		t.Fatalf("circuit-open call should fall back without error: %v", err)
	}
	_, doTLS := stub.counts()
	if doTLS != 1 {
		t.Errorf("expected circuit-open path to delegate to base: doTLS=%d", doTLS)
	}
}

func TestH2FP_CircuitBreaker_ExpiresAfterTTL(t *testing.T) {
	stub := &stubUpstream{}
	svc := newServiceForTest(stub, true, true)
	svc.cfg.Gateway.H2Fingerprint.FallbackErrorThreshold = 1
	svc.cfg.Gateway.H2Fingerprint.FallbackTTLSeconds = 1

	proxyKey := normalizeProxyKey("http://127.0.0.1:1/")
	svc.recordError(proxyKey)
	if !svc.circuitOpen(proxyKey) {
		t.Fatal("circuit should be open after one error with threshold=1")
	}

	// Wait out the TTL.
	time.Sleep(1100 * time.Millisecond)
	if svc.circuitOpen(proxyKey) {
		t.Error("circuit should have closed after TTL")
	}
}

func TestH2FP_CircuitBreaker_SuccessResetsErrorCount(t *testing.T) {
	stub := &stubUpstream{}
	svc := newServiceForTest(stub, true, true)
	svc.cfg.Gateway.H2Fingerprint.FallbackErrorThreshold = 3

	proxyKey := normalizeProxyKey("http://127.0.0.1:1/")
	svc.recordError(proxyKey)
	svc.recordError(proxyKey)
	// Below threshold, not open yet.
	if svc.circuitOpen(proxyKey) {
		t.Fatal("circuit prematurely open")
	}
	svc.recordSuccess(proxyKey)
	svc.recordError(proxyKey)
	svc.recordError(proxyKey)
	// Should still not be open since success reset the counter.
	if svc.circuitOpen(proxyKey) {
		t.Error("success did not reset error count")
	}
}

func TestH2FP_AcquireClient_CachesByKey(t *testing.T) {
	stub := &stubUpstream{}
	svc := newServiceForTest(stub, true, true)

	profile := &tlsfingerprint.Profile{Name: "Node.js"}

	c1, err := svc.acquireClient(1, "http://proxy/", profile)
	if err != nil {
		t.Fatalf("acquire 1: %v", err)
	}
	c2, err := svc.acquireClient(1, "http://proxy/", profile)
	if err != nil {
		t.Fatalf("acquire 2: %v", err)
	}
	if c1 != c2 {
		t.Error("same (account, proxy, profile) should return cached client")
	}

	// Different account → different client.
	c3, err := svc.acquireClient(2, "http://proxy/", profile)
	if err != nil {
		t.Fatalf("acquire 3: %v", err)
	}
	if c1 == c3 {
		t.Error("different accounts should not share client")
	}
}

func TestH2FP_BaseErrorWithFallbackDisabled_Propagates(t *testing.T) {
	// When fallback is off and the BASE upstream itself errors (flag off case),
	// the error must surface as-is — we shouldn't accidentally swallow it.
	wantErr := errors.New("base failed")
	stub := &stubUpstream{returnErr: wantErr}
	svc := newServiceForTest(stub, false, false)

	profile := &tlsfingerprint.Profile{Name: "Node.js"}
	_, err := svc.DoWithTLS(newTestRequest(), "", 1, 1, profile)
	if !errors.Is(err, wantErr) {
		t.Errorf("error should propagate from base: got %v", err)
	}
}

// TestH2FP_InterfaceSatisfied confirms the compile-time assertion holds at runtime.
func TestH2FP_InterfaceSatisfied(t *testing.T) {
	cfg := &config.Config{}
	var up service.HTTPUpstream = NewHTTPUpstreamWithH2Fingerprint(cfg)
	if up == nil {
		t.Fatal("constructor returned nil")
	}
}

// Make sure atomic / sync imports stay used even if helpers shift around.
var _ = atomic.AddInt64
var _ = sync.Mutex{}
