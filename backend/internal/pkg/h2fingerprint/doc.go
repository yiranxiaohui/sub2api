// Package h2fingerprint provides HTTP/2 + header-order fingerprint simulation
// for outbound HTTP requests.
//
// It complements the lower-level tlsfingerprint package, which only controls
// the TLS ClientHello. Once the TLS handshake negotiates ALPN=h2, the wire
// fingerprint continues into the HTTP/2 layer: the initial SETTINGS frame,
// WINDOW_UPDATE, pseudo-header order, and the literal header order. A correct
// TLS fingerprint without a matching h2 fingerprint is itself a strong tell —
// Anthropic / Cloudflare bot detection inspects both layers.
//
// # Node.js 24.x / Claude Code baseline
//
// Values below are captured from a real Claude Code CLI (claude-cli/2.1.81)
// session to api.anthropic.com. They are the defaults applied when a profile
// leaves the corresponding field unset.
//
//	HTTP/2 SETTINGS frame (client → server):
//	  HEADER_TABLE_SIZE      = 65536      // SettingID 0x1
//	  ENABLE_PUSH            = 0          // SettingID 0x2 (disabled)
//	  INITIAL_WINDOW_SIZE    = 6291456    // SettingID 0x4 (6 MiB)
//	  MAX_HEADER_LIST_SIZE   = 65535      // SettingID 0x6
//	(MAX_CONCURRENT_STREAMS and MAX_FRAME_SIZE are NOT sent by Node — omitting
//	them is itself part of the fingerprint.)
//
//	SETTINGS frame order:
//	  [HEADER_TABLE_SIZE, ENABLE_PUSH, INITIAL_WINDOW_SIZE, MAX_HEADER_LIST_SIZE]
//
//	WINDOW_UPDATE on stream 0 immediately after SETTINGS:
//	  increment = 15663105    // ConnectionFlow
//
//	HEADERS frame priority:
//	  Node 24.x does NOT emit explicit PRIORITY for request HEADERS frames.
//
//	Pseudo-header order:
//	  :method → :path → :authority → :scheme
//	(Note: Go's stdlib http2 fixes this as :method → :authority → :scheme → :path;
//	 the difference is fingerprintable.)
//
// # Comparison with Go stdlib
//
//	                          Node.js 24.x       Go stdlib http2
//	HEADER_TABLE_SIZE         65536              4096
//	ENABLE_PUSH               0 (sent)           1 (omitted by clients)
//	INITIAL_WINDOW_SIZE       6291456            4194304
//	MAX_FRAME_SIZE            (omitted)          16384
//	MAX_HEADER_LIST_SIZE      65535              10485760
//	Connection-level WINDOW   15663105 (sent)    1048576 (sent)
//	Pseudo-header order       method→path→...    method→authority→...
//
// # Scope of this package
//
// This package only describes the data and provides helpers. The actual
// dialing/handshake/wire framing happens in a Client implementation (see
// client.go) that wires together:
//
//   - tlsfingerprint.BuildClientHelloSpec for the ClientHello
//   - an underlying HTTP client library that exposes h2 SETTINGS, header
//     order, and pseudo-header order knobs (currently imroc/req/v3)
package h2fingerprint
