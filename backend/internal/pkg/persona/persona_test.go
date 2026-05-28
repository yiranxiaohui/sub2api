package persona

import (
	"strings"
	"testing"
)

func TestBuildUserAgent(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"2.1.92", "claude-cli/2.1.92 (external, cli)"},
		{"2.1.70", "claude-cli/2.1.70 (external, cli)"},
		{"3.0.0", "claude-cli/3.0.0 (external, cli)"},
	}
	for _, c := range cases {
		if got := BuildUserAgent(c.in); got != c.want {
			t.Errorf("BuildUserAgent(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func validPersonaFixture() Persona {
	return Persona{
		ClientID:                strings.Repeat("a", 64),
		OS:                      "MacOS",
		Arch:                    "arm64",
		KernelVersion:           "23.6.0",
		CPUModel:                "Apple M2",
		Hostname:                "mbp-7a3f",
		CLIVersion:              "2.1.92",
		UserAgent:               BuildUserAgent("2.1.92"),
		StainlessLang:           "js",
		StainlessRuntime:        "node",
		StainlessRuntimeVersion: "v20.18.1",
		StainlessPackageVersion: "2.1.92",
		StainlessOS:             "MacOS",
		StainlessArch:           "arm64",
		LocaleLang:              "en-US",
		Timezone:                "America/Los_Angeles",
		BetaVariantID:           "thinking",
		TLSProfileID:            0,
		CacheBreakpointVariant:  "default",
		PoolID:                  "p1234567890ab",
		SchemaVersion:           SchemaVersion,
		UpdatedAt:               1700000000,
	}
}

func TestPersonaValidate_Happy(t *testing.T) {
	p := validPersonaFixture()
	if err := p.Validate(); err != nil {
		t.Errorf("valid persona failed Validate: %v", err)
	}
}

func TestPersonaValidate_RejectsBadOS(t *testing.T) {
	p := validPersonaFixture()
	p.OS = "FreeBSD"
	if err := p.Validate(); err == nil {
		t.Error("expected error for unsupported OS")
	}
}

func TestPersonaValidate_RejectsBadArch(t *testing.T) {
	p := validPersonaFixture()
	p.Arch = "mips"
	p.StainlessArch = "mips"
	if err := p.Validate(); err == nil {
		t.Error("expected error for unsupported Arch")
	}
}

func TestPersonaValidate_RejectsAppleSiliconOnX64(t *testing.T) {
	p := validPersonaFixture()
	p.Arch = "x64"
	p.StainlessArch = "x64"
	p.KernelVersion = "23.6.0" // still macOS-shaped
	// CPU stays Apple M2 — that's the inconsistency
	err := p.Validate()
	if err == nil {
		t.Error("expected error for Apple silicon on x64")
	}
	if err != nil && !strings.Contains(err.Error(), "Apple") {
		t.Errorf("error message should mention Apple: %v", err)
	}
}

func TestPersonaValidate_RejectsIntelOnArm64(t *testing.T) {
	p := validPersonaFixture()
	p.CPUModel = "Intel Core i7-12700H"
	err := p.Validate()
	if err == nil {
		t.Error("expected error for Intel CPU on arm64")
	}
}

func TestPersonaValidate_RejectsBadDarwinKernel(t *testing.T) {
	p := validPersonaFixture()
	p.KernelVersion = "1.2.3" // first component < 20 → not Darwin shape
	if err := p.Validate(); err == nil {
		t.Error("expected error for non-Darwin kernel on macOS")
	}
}

func TestPersonaValidate_RejectsBadLinuxKernel(t *testing.T) {
	p := validPersonaFixture()
	p.OS = "Linux"
	p.StainlessOS = "Linux"
	p.CPUModel = "AMD Ryzen 7 7840U"
	p.Arch = "x64"
	p.StainlessArch = "x64"
	p.KernelVersion = "not-a-kernel"
	if err := p.Validate(); err == nil {
		t.Error("expected error for malformed Linux kernel")
	}
}

func TestPersonaValidate_RejectsWindowsKernelMismatch(t *testing.T) {
	p := validPersonaFixture()
	p.OS = "Windows"
	p.StainlessOS = "Windows"
	p.Arch = "x64"
	p.StainlessArch = "x64"
	p.CPUModel = "Intel Core i7-12700H"
	p.KernelVersion = "23.6.0" // Darwin shape on Windows = bug
	if err := p.Validate(); err == nil {
		t.Error("expected error for Darwin kernel on Windows")
	}
}

func TestPersonaValidate_RequiresUAMatchesCLIVersion(t *testing.T) {
	p := validPersonaFixture()
	p.CLIVersion = "2.1.92"
	p.UserAgent = "claude-cli/9.9.9 (external, cli)"
	if err := p.Validate(); err == nil {
		t.Error("expected error for UA/CLIVersion mismatch")
	}
}

func TestPersonaValidate_RequiresStainlessOSMatchesOS(t *testing.T) {
	p := validPersonaFixture()
	p.StainlessOS = "Linux"
	if err := p.Validate(); err == nil {
		t.Error("expected error for StainlessOS != OS")
	}
}

func TestPersonaValidate_RequiresClientIDLength(t *testing.T) {
	p := validPersonaFixture()
	p.ClientID = "short"
	if err := p.Validate(); err == nil {
		t.Error("expected error for short ClientID")
	}
	p.ClientID = strings.Repeat("a", 63)
	if err := p.Validate(); err == nil {
		t.Error("expected error for 63-char ClientID")
	}
}

func TestPersonaValidate_RequiresSchemaVersion(t *testing.T) {
	p := validPersonaFixture()
	p.SchemaVersion = 0
	if err := p.Validate(); err == nil {
		t.Error("expected error for zero SchemaVersion")
	}
}

// looksLikeDarwinKernel-specific edge cases — easy to get wrong in maintenance
func TestLooksLikeDarwinKernel_TableDriven(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"23.6.0", true},
		{"24.1.0", true},
		{"20.0.0", true},
		{"5.15.0", false}, // looks like Linux
		{"23.6", false},   // wrong shape
		{"23.6.0.0", false},
		{"", false},
		{"abc.def.ghi", false},
		{"19.6.0", false}, // pre-macOS-11 reject (below Darwin 20)
	}
	for _, c := range cases {
		got := looksLikeDarwinKernel(c.in)
		if got != c.want {
			t.Errorf("looksLikeDarwinKernel(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestLooksLikeLinuxKernel_TableDriven(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"6.5.0-15-generic", true},
		{"5.15.0-105-generic", true},
		{"6.1.0-21-amd64", true},
		{"6.6.0-asahi", true},
		{"23.6.0", true}, // structurally OK at this level (looksLike functions are lenient)
		{"abc", false},
		{"", false},
		{"6", false}, // no dot
	}
	for _, c := range cases {
		got := looksLikeLinuxKernel(c.in)
		if got != c.want {
			t.Errorf("looksLikeLinuxKernel(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}
