package h2fingerprint

// HTTP/2 SETTINGS identifiers as defined in RFC 7540 §6.5.2.
// We redeclare them here as plain uint16 to avoid pulling in a specific http2
// implementation at the type level — the actual SETTINGS frame is written by
// whichever HTTP client library backs the Client; this package only carries
// data.
const (
	SettingHeaderTableSize      uint16 = 0x1
	SettingEnablePush           uint16 = 0x2
	SettingMaxConcurrentStreams uint16 = 0x3
	SettingInitialWindowSize    uint16 = 0x4
	SettingMaxFrameSize         uint16 = 0x5
	SettingMaxHeaderListSize    uint16 = 0x6
)

// Setting is one entry in the HTTP/2 SETTINGS frame.
// Order matters — Node.js sends settings in a specific sequence and that
// sequence is part of the fingerprint.
type Setting struct {
	ID    uint16
	Value uint32
}

// H2Profile captures the HTTP/2-layer fingerprint of a real client.
//
// A zero value selects the Node.js 24.x baseline (see Defaults). Callers
// that want to override only specific fields should start from Defaults() and
// mutate, rather than constructing an H2Profile from scratch — leaving
// SettingsOrder or PseudoHeaderOrder empty produces an invalid fingerprint.
type H2Profile struct {
	// Name identifies the profile in logs and metrics. Not transmitted.
	Name string

	// Settings is the list of SETTINGS frame entries (in send order).
	// nil or empty falls back to Defaults().Settings.
	Settings []Setting

	// ConnectionFlow is the WINDOW_UPDATE increment sent on stream 0
	// immediately after SETTINGS. Zero falls back to Defaults().ConnectionFlow.
	ConnectionFlow uint32

	// PseudoHeaderOrder is the order of :method / :authority / :scheme / :path
	// in HEADERS frames. Empty falls back to Defaults().PseudoHeaderOrder.
	PseudoHeaderOrder []string

	// HeaderOrder is the order of regular (non-pseudo) headers in HEADERS
	// frames. Empty means "use the order embedded in the http.Request"
	// (which by itself is map-iteration order — usually wrong; callers
	// should populate this from the captured wire order of the client
	// they are impersonating).
	HeaderOrder []string

	// EmitHeaderPriority controls whether to emit an explicit PRIORITY
	// segment in HEADERS frames. Node.js 24.x does NOT — keep false.
	EmitHeaderPriority bool
}

// nodeJS24Settings is the SETTINGS frame Claude Code (Node.js 24.x undici)
// sends as its initial client preface. Order matters.
var nodeJS24Settings = []Setting{
	{ID: SettingHeaderTableSize, Value: 65536},
	{ID: SettingEnablePush, Value: 0},
	{ID: SettingInitialWindowSize, Value: 6291456},
	{ID: SettingMaxHeaderListSize, Value: 65535},
}

// nodeJS24PseudoHeaderOrder is the order Node.js places pseudo-headers in
// HEADERS frames. Differs from Go stdlib http2 (which uses method→authority→scheme→path).
var nodeJS24PseudoHeaderOrder = []string{":method", ":path", ":authority", ":scheme"}

// claudeCLI2WireHeaderOrder is the regular-header order observed in a
// claude-cli/2.1.81 capture against api.anthropic.com. Mirrors
// service.headerWireOrder but redeclared here so this package has no upward
// dependency on the service layer.
//
// When a profile is built for a different CLI version, callers should
// override this with the matching capture — header order shifts across CLI
// releases.
var claudeCLI2WireHeaderOrder = []string{
	"Accept",
	"X-Stainless-Retry-Count",
	"X-Stainless-Timeout",
	"X-Stainless-Lang",
	"X-Stainless-Package-Version",
	"X-Stainless-OS",
	"X-Stainless-Arch",
	"X-Stainless-Runtime",
	"X-Stainless-Runtime-Version",
	"anthropic-dangerous-direct-browser-access",
	"anthropic-version",
	"authorization",
	"x-app",
	"User-Agent",
	"X-Claude-Code-Session-Id",
	"content-type",
	"anthropic-beta",
	"x-client-request-id",
	"accept-language",
	"sec-fetch-mode",
	"accept-encoding",
	"content-length",
	"x-stainless-helper-method",
}

// Defaults returns a fresh H2Profile populated with the Node.js 24.x /
// claude-cli/2.1.x baseline. The returned value is a copy — mutating it does
// not affect subsequent calls.
func Defaults() H2Profile {
	settings := make([]Setting, len(nodeJS24Settings))
	copy(settings, nodeJS24Settings)

	pseudoOrder := make([]string, len(nodeJS24PseudoHeaderOrder))
	copy(pseudoOrder, nodeJS24PseudoHeaderOrder)

	headerOrder := make([]string, len(claudeCLI2WireHeaderOrder))
	copy(headerOrder, claudeCLI2WireHeaderOrder)

	return H2Profile{
		Name:               "Node.js 24.x / claude-cli 2.1",
		Settings:           settings,
		ConnectionFlow:     15663105,
		PseudoHeaderOrder:  pseudoOrder,
		HeaderOrder:        headerOrder,
		EmitHeaderPriority: false,
	}
}

// Resolved returns a copy of p with any zero/empty fields filled in from
// Defaults(). Use this at the boundary between configuration and the wire
// layer so client code can always assume every field is populated.
func (p H2Profile) Resolved() H2Profile {
	d := Defaults()
	if p.Name == "" {
		p.Name = d.Name
	}
	if len(p.Settings) == 0 {
		p.Settings = d.Settings
	}
	if p.ConnectionFlow == 0 {
		p.ConnectionFlow = d.ConnectionFlow
	}
	if len(p.PseudoHeaderOrder) == 0 {
		p.PseudoHeaderOrder = d.PseudoHeaderOrder
	}
	if len(p.HeaderOrder) == 0 {
		p.HeaderOrder = d.HeaderOrder
	}
	return p
}
