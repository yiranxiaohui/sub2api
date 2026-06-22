package h2fingerprint

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"time"

	"github.com/imroc/req/v3"
	"github.com/imroc/req/v3/http2"
	utls "github.com/refraction-networking/utls"

	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
)

// Options configures a fingerprinted req client.
//
// A zero value yields a Node.js 24.x / claude-cli baseline client with no
// proxy and no timeout — useful for unit tests but the gateway hot path
// should always set Timeout and ProxyURL explicitly.
type Options struct {
	// TLSProfile is the utls ClientHello spec to use. nil means "use the
	// tlsfingerprint package built-in Node.js 24.x default" — the same
	// default applied by tlsfingerprint.NewDialer(nil, ...).
	TLSProfile *tlsfingerprint.Profile

	// H2Profile is the HTTP/2 wire fingerprint. Zero-value fields fall back
	// to Defaults() via H2Profile.Resolved.
	H2Profile H2Profile

	// ProxyURL is the optional outbound proxy (http://, https://, socks5://, socks5h://).
	// Empty means direct. The TLS handshake to the target server runs after the
	// proxy CONNECT tunnel is established, so the upstream sees only our utls
	// fingerprint, never the proxy's.
	ProxyURL string

	// Timeout is the overall request timeout. Zero means no client-level timeout
	// (the per-request context still applies).
	Timeout time.Duration

	// TLSHandshakeTimeout caps the utls handshake. Zero defaults to req's
	// default (10s).
	TLSHandshakeTimeout time.Duration
}

// NewClient builds a *req.Client that emits the Node.js 24.x / claude-cli wire
// fingerprint: utls ClientHello, HTTP/2 SETTINGS / WINDOW_UPDATE, pseudo-header
// order, and regular header order.
//
// Callers send actual HTTP requests with c.R() / c.Do() exactly as with a
// stock req.Client; the fingerprint is applied transparently on every
// connection.
//
// The returned client is safe for concurrent use. It is the caller's
// responsibility to cache and reuse it — building a fresh client on every
// request defeats connection pooling and the TLS session ticket reuse that
// makes the fingerprint cheap.
func NewClient(opts Options) (*req.Client, error) {
	h2 := opts.H2Profile.Resolved()

	// Translate our profile-package Setting (with raw uint16 IDs to avoid
	// pulling http2 types into the data layer) into req's bundled http2.Setting.
	settings := make([]http2.Setting, len(h2.Settings))
	for i, s := range h2.Settings {
		settings[i] = http2.Setting{ID: http2.SettingID(s.ID), Val: s.Value}
	}

	tlsProfile, err := resolveALPN(opts.TLSProfile)
	if err != nil {
		return nil, err
	}

	c := req.C().
		EnableForceHTTP2().
		SetHTTP2SettingsFrame(settings...).
		SetHTTP2ConnectionFlow(h2.ConnectionFlow).
		// NB: upstream method name is "Oder" — typo in github.com/imroc/req/v3
		// up to at least v3.57.0. Tracked as upstream issue; do not "fix"
		// without also bumping the dependency.
		SetCommonPseudoHeaderOder(h2.PseudoHeaderOrder...).
		SetCommonHeaderOrder(h2.HeaderOrder...).
		SetTLSHandshake(buildTLSHandshakeFunc(tlsProfile))

	if opts.Timeout > 0 {
		c.SetTimeout(opts.Timeout)
	}
	if opts.TLSHandshakeTimeout > 0 {
		c.SetTLSHandshakeTimeout(opts.TLSHandshakeTimeout)
	}
	if opts.ProxyURL != "" {
		c.SetProxyURL(opts.ProxyURL)
	}

	return c, nil
}

// resolveALPN returns a copy of profile with ALPNProtocols guaranteed to start
// with "h2", which is required for the HTTP/2 framer to take over. We do this
// here rather than mutating tlsfingerprint.Profile defaults because other call
// sites of that package still want http/1.1 only.
func resolveALPN(profile *tlsfingerprint.Profile) (*tlsfingerprint.Profile, error) {
	var p tlsfingerprint.Profile
	if profile != nil {
		p = *profile
	}

	// If the caller didn't pin ALPN, set the Node.js default ["h2", "http/1.1"].
	if len(p.ALPNProtocols) == 0 {
		p.ALPNProtocols = []string{"h2", "http/1.1"}
		return &p, nil
	}

	// Caller pinned ALPN — sanity-check that h2 is offered, otherwise this
	// client is misconfigured (would silently fall back to HTTP/1.1).
	for _, proto := range p.ALPNProtocols {
		if proto == "h2" {
			return &p, nil
		}
	}
	return nil, fmt.Errorf("h2fingerprint: TLSProfile.ALPNProtocols does not include \"h2\"; got %v", p.ALPNProtocols)
}

// buildTLSHandshakeFunc returns a req.SetTLSHandshake-compatible callback that
// performs the utls handshake against an already-connected TCP (or tunneled)
// conn.
func buildTLSHandshakeFunc(profile *tlsfingerprint.Profile) func(ctx context.Context, addr string, plainConn net.Conn) (net.Conn, *tls.ConnectionState, error) {
	return func(ctx context.Context, addr string, plainConn net.Conn) (net.Conn, *tls.ConnectionState, error) {
		host, _, err := net.SplitHostPort(addr)
		if err != nil {
			host = addr
		}

		spec := tlsfingerprint.BuildClientHelloSpec(profile)

		uconn := utls.UClient(plainConn, &utls.Config{ServerName: host}, utls.HelloCustom)
		if err := uconn.ApplyPreset(spec); err != nil {
			_ = plainConn.Close()
			return nil, nil, fmt.Errorf("h2fingerprint: apply utls preset: %w", err)
		}
		if err := uconn.HandshakeContext(ctx); err != nil {
			_ = plainConn.Close()
			return nil, nil, fmt.Errorf("h2fingerprint: utls handshake: %w", err)
		}

		state := convertConnectionState(uconn.ConnectionState())
		return uconn, &state, nil
	}
}

// convertConnectionState translates utls.ConnectionState into the stdlib
// tls.ConnectionState type that req's HTTP layer expects. The two structs
// share the same field names for everything req actually inspects
// (NegotiatedProtocol, Version, CipherSuite, ServerName, certificates).
// utls-only fields (PeerApplicationSettings, ECHRetryConfigs) are dropped.
func convertConnectionState(s utls.ConnectionState) tls.ConnectionState {
	return tls.ConnectionState{
		Version:                     s.Version,
		HandshakeComplete:           s.HandshakeComplete,
		DidResume:                   s.DidResume,
		CipherSuite:                 s.CipherSuite,
		NegotiatedProtocol:          s.NegotiatedProtocol,
		ServerName:                  s.ServerName,
		PeerCertificates:            s.PeerCertificates,
		VerifiedChains:              s.VerifiedChains,
		SignedCertificateTimestamps: s.SignedCertificateTimestamps,
		OCSPResponse:                s.OCSPResponse,
		TLSUnique:                   s.TLSUnique,
	}
}
