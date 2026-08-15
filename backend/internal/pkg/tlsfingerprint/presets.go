package tlsfingerprint

// BuiltInDefaultProfileName is the human-readable name of the ClientHello
// profile shipped with Sub2API. A TLS fingerprint identifies a client runtime,
// not a physical device, so the same profile can safely be reused by many
// accounts running the same Claude Code generation.
const BuiltInDefaultProfileName = "Claude Code / Node.js 24.x"

// BuiltInDefaultProfile returns a fully materialized copy of the built-in
// Claude Code ClientHello profile.
//
// ALPNProtocols is intentionally left empty. The HTTP/1.1 transport resolves
// it to ["http/1.1"], while the HTTP/2 fingerprint transport resolves it to
// ["h2", "http/1.1"]. Keeping ALPN transport-aware prevents a generated
// profile from advertising a protocol the selected transport cannot speak.
func BuiltInDefaultProfile() *Profile {
	curves := make([]uint16, len(defaultCurves))
	for i, curve := range defaultCurves {
		curves[i] = uint16(curve)
	}

	signatureAlgorithms := make([]uint16, len(defaultSignatureAlgorithms))
	for i, algorithm := range defaultSignatureAlgorithms {
		signatureAlgorithms[i] = uint16(algorithm)
	}

	return &Profile{
		Name:                BuiltInDefaultProfileName,
		CipherSuites:        append([]uint16(nil), defaultCipherSuites...),
		Curves:              curves,
		PointFormats:        append([]uint16(nil), defaultPointFormats...),
		EnableGREASE:        false,
		SignatureAlgorithms: signatureAlgorithms,
		SupportedVersions:   []uint16{0x0304, 0x0303},
		KeyShareGroups:      []uint16{29},
		PSKModes:            []uint16{1},
		Extensions:          append([]uint16(nil), defaultExtensionOrder...),
	}
}
