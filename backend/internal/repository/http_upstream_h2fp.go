package repository

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/imroc/req/v3"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/h2fingerprint"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

// h2fpUpstreamService wraps a base HTTPUpstream with an HTTP/2 + utls
// fingerprint path for Anthropic-style requests.
//
// Dispatch rules:
//
//   - Do(...)            → always delegate to base (no fingerprint).
//   - DoWithTLS(profile=nil) → delegate to base.Do (matches base.DoWithTLS shortcut).
//   - DoWithTLS(profile!=nil) and feature flag OFF → delegate to base.DoWithTLS.
//   - DoWithTLS(profile!=nil) and feature flag ON  → route through h2fp client.
//
// On request failure with FallbackOnError set, the request is retried once
// through the base stdlib path so a transient h2fp issue doesn't surface to
// the caller. Repeated failures trigger a per-proxy circuit breaker that
// keeps the path on stdlib for FallbackTTLSeconds.
type h2fpUpstreamService struct {
	cfg  *config.Config
	base service.HTTPUpstream

	clients  sync.Map // key: h2fpClientKey → *req.Client
	circuits sync.Map // key: proxyKey      → *h2fpCircuit
}

type h2fpCircuit struct {
	mu            sync.Mutex
	windowStart   time.Time
	errorCount    int
	fallbackUntil time.Time
}

// NewHTTPUpstreamWithH2Fingerprint builds an HTTPUpstream that opportunistically
// applies the Node.js / claude-cli HTTP/2 wire fingerprint to TLS-fingerprinted
// requests. When the feature flag is disabled the returned upstream behaves
// identically to NewHTTPUpstream — this is the single wiring point used by the
// DI graph.
func NewHTTPUpstreamWithH2Fingerprint(cfg *config.Config) service.HTTPUpstream {
	base := NewHTTPUpstream(cfg)
	return &h2fpUpstreamService{
		cfg:  cfg,
		base: base,
	}
}

// Do — non-fingerprinted path is unchanged.
func (s *h2fpUpstreamService) Do(req *http.Request, proxyURL string, accountID int64, accountConcurrency int) (*http.Response, error) {
	return s.base.Do(req, proxyURL, accountID, accountConcurrency)
}

// DoWithTLS routes through the h2 fingerprint stack when the feature is on
// and the caller provided a TLS profile. Anything else delegates to the base
// implementation so behaviour matches existing tests.
func (s *h2fpUpstreamService) DoWithTLS(req *http.Request, proxyURL string, accountID int64, accountConcurrency int, profile *tlsfingerprint.Profile) (*http.Response, error) {
	if profile == nil || !s.enabled() {
		return s.base.DoWithTLS(req, proxyURL, accountID, accountConcurrency, profile)
	}
	// Plain HTTP has no TLS handshake to fingerprint. Reuse the base path so
	// behavior matches base.DoWithTLS (which delegates to Do for http URLs).
	if req != nil && req.URL != nil && strings.EqualFold(req.URL.Scheme, "http") {
		return s.base.DoWithTLS(req, proxyURL, accountID, accountConcurrency, profile)
	}

	// The stdlib path guards SSRF / private-IP dialing (validateRequestHost).
	// The h2fp path dials through req's transport, so it must apply the same
	// guard itself — otherwise enabling h2fp would silently skip the
	// URLAllowlist check.
	if shouldValidateResolvedIP(s.cfg) {
		if err := validateRequestHost(req, s.cfg); err != nil {
			return nil, err
		}
	}
	// Keep identity-header injection in sync with the stdlib DoWithTLS path.
	applyGrokCLIProxyHeaders(req)

	proxyKey := normalizeProxyKey(proxyURL)
	if s.circuitOpen(proxyKey) {
		slog.Debug("h2fp_circuit_open_falling_back", "account_id", accountID, "proxy", proxyKey)
		return s.base.DoWithTLS(req, proxyURL, accountID, accountConcurrency, profile)
	}

	client, err := s.acquireClient(accountID, proxyURL, profile, accountConcurrency)
	if err != nil {
		s.recordError(proxyKey)
		if s.cfg.Gateway.H2Fingerprint.FallbackOnError {
			slog.Debug("h2fp_acquire_client_failed_falling_back", "account_id", accountID, "error", err)
			return s.base.DoWithTLS(req, proxyURL, accountID, accountConcurrency, profile)
		}
		return nil, fmt.Errorf("h2fp acquire client: %w", err)
	}

	var fallbackReq *http.Request
	if s.cfg.Gateway.H2Fingerprint.FallbackOnError {
		fallbackReq = cloneRequestForReplay(req)
		if fallbackReq != nil && fallbackReq.Body != nil {
			defer func() { _ = fallbackReq.Body.Close() }()
		}
	}

	resp, err := client.Do(req)
	if err != nil {
		s.recordError(proxyKey)
		if s.cfg.Gateway.H2Fingerprint.FallbackOnError && fallbackReq != nil {
			slog.Debug("h2fp_request_failed_falling_back", "account_id", accountID, "error", err)
			// Note: req's Do is expected not to write to resp on error, but
			// guard against a partially-populated body just in case.
			if resp != nil && resp.Body != nil {
				_ = resp.Body.Close()
			}
			return s.base.DoWithTLS(fallbackReq, proxyURL, accountID, accountConcurrency, profile)
		}
		return nil, err
	}

	s.recordSuccess(proxyKey)
	// req auto-decompresses by default; the base path's manual decompress
	// helper is only needed when the caller pre-set Accept-Encoding and stdlib
	// declines to handle it. We mirror that here for parity.
	decompressResponseBody(resp)
	return resp, nil
}

// cloneRequestForReplay prepares a second request before the h2fp attempt can
// consume or close the original body. A nil result means the body is not
// replayable, so retrying would risk sending an empty or partial payload.
func cloneRequestForReplay(req *http.Request) *http.Request {
	if req == nil {
		return nil
	}
	clone := req.Clone(req.Context())
	if req.Body == nil || req.Body == http.NoBody {
		clone.Body = req.Body
		return clone
	}
	if req.GetBody == nil {
		return nil
	}
	body, err := req.GetBody()
	if err != nil {
		return nil
	}
	clone.Body = body
	return clone
}

func (s *h2fpUpstreamService) enabled() bool {
	if s.cfg == nil {
		return false
	}
	return s.cfg.Gateway.H2Fingerprint.Enabled
}

// ---- client cache ----

type h2fpClientKey struct {
	accountID int64
	proxyKey  string
	// profileID is a stable content hash of the TLS profile's ClientHello
	// fields (see profileCacheKey). Editing or rebinding a profile therefore
	// builds a fresh client instead of reusing a stale one.
	profileID string
}

func (s *h2fpUpstreamService) acquireClient(accountID int64, proxyURL string, profile *tlsfingerprint.Profile, accountConcurrency int) (*req.Client, error) {
	key := h2fpClientKey{
		accountID: accountID,
		proxyKey:  normalizeProxyKey(proxyURL),
		profileID: profileCacheKey(profile),
	}
	if cached, ok := s.clients.Load(key); ok {
		if c, ok := cached.(*req.Client); ok {
			return c, nil
		}
	}
	c, err := h2fingerprint.NewClient(h2fingerprint.Options{
		TLSProfile: profile,
		ProxyURL:   strings.TrimSpace(proxyURL),
	})
	if err != nil {
		return nil, err
	}
	c.GetClient().CheckRedirect = s.h2RedirectChecker
	// Mirror the stdlib path's connection-pool sizing (including the
	// account-concurrency scaling) so the h2fp layer reuses connections the
	// same way instead of relying on req's defaults.
	s.applyPoolToClient(c, accountConcurrency)
	actual, _ := s.clients.LoadOrStore(key, c)
	if cached, ok := actual.(*req.Client); ok {
		return cached, nil
	}
	return c, nil
}

func (s *h2fpUpstreamService) h2RedirectChecker(req *http.Request, via []*http.Request) error {
	if req != nil && service.HTTPUpstreamRedirectsDisabled(req.Context()) {
		return http.ErrUseLastResponse
	}
	if len(via) >= 10 {
		return errors.New("stopped after 10 redirects")
	}
	return validateRequestHost(req, s.cfg)
}

// applyPoolToClient replicates the stdlib transport's pool settings
// (defaultPoolSettings + account-concurrency scaling, see httpUpstreamService.
// resolvePoolSettings) on a req client. All five fields live on req's shared
// transport.Options, so setting them also applies to the internal http2
// transport.
func (s *h2fpUpstreamService) applyPoolToClient(c *req.Client, accountConcurrency int) {
	if s.cfg == nil {
		return
	}
	settings := defaultPoolSettings(s.cfg)
	if accountConcurrency > 0 {
		settings.maxIdleConns = accountConcurrency
		settings.maxIdleConnsPerHost = accountConcurrency
		settings.maxConnsPerHost = accountConcurrency
	}
	tr := c.GetTransport()
	tr.SetMaxIdleConns(settings.maxIdleConns)
	// No dedicated setter for this field in req v3.57.0; it is exported on the
	// embedded transport.Options, so set it directly.
	tr.MaxIdleConnsPerHost = settings.maxIdleConnsPerHost
	tr.SetMaxConnsPerHost(settings.maxConnsPerHost)
	tr.SetIdleConnTimeout(settings.idleConnTimeout)
	tr.SetResponseHeaderTimeout(settings.responseHeaderTimeout)
}

func normalizeProxyKey(proxyURL string) string {
	trimmed := strings.TrimSpace(proxyURL)
	if trimmed == "" {
		return directProxyKey
	}
	// Reuse the existing normalizer used by stdlib path so the two layers'
	// circuit-breaker keys line up if anyone wants to correlate logs.
	if normalized, _, err := normalizeProxyURL(trimmed); err == nil && normalized != "" {
		return normalized
	}
	return trimmed
}

// ---- circuit breaker ----

func (s *h2fpUpstreamService) circuitOpen(proxyKey string) bool {
	raw, ok := s.circuits.Load(proxyKey)
	if !ok {
		return false
	}
	c, ok := raw.(*h2fpCircuit)
	if !ok {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.fallbackUntil.IsZero() {
		return false
	}
	if time.Now().Before(c.fallbackUntil) {
		return true
	}
	// TTL expired — reset so the next call gets a fresh chance.
	c.fallbackUntil = time.Time{}
	c.errorCount = 0
	c.windowStart = time.Time{}
	return false
}

func (s *h2fpUpstreamService) recordError(proxyKey string) {
	cfg := s.cfg.Gateway.H2Fingerprint
	if cfg.FallbackErrorThreshold <= 0 {
		return
	}
	raw, _ := s.circuits.LoadOrStore(proxyKey, &h2fpCircuit{})
	c, ok := raw.(*h2fpCircuit)
	if !ok {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now()
	window := time.Duration(cfg.FallbackWindowSeconds) * time.Second
	if window <= 0 || c.windowStart.IsZero() || now.Sub(c.windowStart) > window {
		c.windowStart = now
		c.errorCount = 1
	} else {
		c.errorCount++
	}

	if c.errorCount >= cfg.FallbackErrorThreshold {
		ttl := time.Duration(cfg.FallbackTTLSeconds) * time.Second
		if ttl <= 0 {
			ttl = 10 * time.Minute
		}
		c.fallbackUntil = now.Add(ttl)
		slog.Warn("h2fp_circuit_tripped", "proxy", proxyKey, "errors", c.errorCount, "ttl", ttl)
	}
}

func (s *h2fpUpstreamService) recordSuccess(proxyKey string) {
	raw, ok := s.circuits.Load(proxyKey)
	if !ok {
		return
	}
	c, ok := raw.(*h2fpCircuit)
	if !ok {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	// A single success resets the window; we are optimistic by design.
	c.errorCount = 0
	c.windowStart = time.Time{}
	c.fallbackUntil = time.Time{}
}

// Compile-time guard: h2fpUpstreamService must implement service.HTTPUpstream.
var _ service.HTTPUpstream = (*h2fpUpstreamService)(nil)

// ensure context is imported even if the package layout shifts.
var _ context.Context = nil

// errH2FPDisabled is returned when the caller forces the h2fp path off via a
// hint and the fallback chain is unavailable (defensive only — current code
// always has a base upstream).
var errH2FPDisabled = errors.New("h2fp: disabled")

var _ = errH2FPDisabled
