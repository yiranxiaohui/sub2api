package claude

// nativeCLIProfileVersion identifies the capture backing the built-in defaults.
// It is a CLI protocol version, not the gateway release version.
const nativeCLIProfileVersion = "2.1.327"

// ClientProfile contains the non-secret, installation-level SDK header values
// observed in a native Linux x64 CLI capture. Account/session IDs, feature flags,
// beta capabilities and TLS state deliberately do not belong in this profile.
type ClientProfile struct {
	CLIVersion              string
	StainlessLang           string
	StainlessPackageVersion string
	StainlessOS             string
	StainlessArch           string
	StainlessRuntime        string
	StainlessRuntimeVersion string
}

// DefaultClientProfile returns a value copy so callers cannot mutate the shared
// defaults. Native CLI 2.1.327 reports "node" even though it ships as an ELF.
// A runtime UA override does not establish measurements for a newer SDK: these
// values remain the last measured baseline until another capture is reviewed.
func DefaultClientProfile() ClientProfile {
	return ClientProfile{
		CLIVersion:              nativeCLIProfileVersion,
		StainlessLang:           "js",
		StainlessPackageVersion: "0.128.0",
		StainlessOS:             "Linux",
		StainlessArch:           "x64",
		StainlessRuntime:        "node",
		StainlessRuntimeVersion: "v26.12.1",
	}
}
