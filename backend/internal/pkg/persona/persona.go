// Package persona generates per-account device personas used to make accounts
// look like distinct real machines rather than copies of the same backend.
//
// A Persona collects every account-scoped detail an upstream might use to
// cluster accounts together: OS / Arch / Kernel / CPU model, CLI version,
// Stainless SDK metadata, anthropic-beta flag set, TLS profile assignment,
// prompt cache breakpoint strategy, and a locale + timezone hint.
//
// The package is data-only — it does not persist Personas, talk to Redis, or
// touch HTTP requests. Higher layers (identity_service) own persistence and
// application. The package does enforce *internal* consistency constraints
// (e.g. Apple M-series CPUs only on arm64) — see [Persona.Validate].
//
// # Determinism
//
// [SelectPersona] is a pure function of (accountID, pool). Given the same pool
// configuration, the same account always resolves to the same Persona, even
// across cache flushes, DB rebuilds, or process restarts. This is critical:
// an account whose OS/Arch flickers between requests is far more suspicious
// to Anthropic than 50 accounts that all look like Macs.
package persona

import (
	"fmt"
	"strings"
)

// SchemaVersion is bumped whenever the Persona wire format changes in a way
// that requires regeneration. Bumping it forces all cached Personas to be
// re-derived on next read.
const SchemaVersion = 1

// Persona captures every per-account identity dimension that goes onto the
// wire. Fields fall into three groups:
//
//   - Machine identity:     OS, Arch, KernelVersion, CPUModel, Hostname, Runtime
//   - SDK / CLI metadata:   CLIVersion, UserAgent, Stainless*
//   - Locale & behavior:    LocaleLang, Timezone, BetaVariantID, TLSProfileID,
//     CacheBreakpointVariant
//
// ClientID is the 64-char hex "device id" carried in metadata.user_id; it
// existed in the older Fingerprint struct and remains here for back-compat.
type Persona struct {
	// --- back-compat: was Fingerprint.ClientID ---
	ClientID string `json:"client_id"`

	// --- machine identity ---
	OS            string `json:"os"`             // "MacOS" / "Linux" / "Windows"
	Arch          string `json:"arch"`           // "arm64" / "x64"
	KernelVersion string `json:"kernel_version"` // "23.6.0" / "6.5.0-15-generic"
	CPUModel      string `json:"cpu_model"`      // "Apple M2" / "Intel Core i7-12700H"
	Hostname      string `json:"hostname"`       // "mbp-7a3f"

	// --- SDK / CLI ---
	CLIVersion              string `json:"cli_version"`               // "2.1.92"
	UserAgent               string `json:"user_agent"`                // derived: "claude-cli/2.1.92 (external, cli)"
	StainlessLang           string `json:"stainless_lang"`            // "js"
	StainlessRuntime        string `json:"stainless_runtime"`         // "node"
	StainlessRuntimeVersion string `json:"stainless_runtime_version"` // "v20.18.1"
	StainlessPackageVersion string `json:"stainless_package_version"` // matches CLI
	StainlessOS             string `json:"stainless_os"`              // "MacOS"
	StainlessArch           string `json:"stainless_arch"`            // "arm64"

	// --- locale & behavior ---
	LocaleLang             string `json:"locale_lang"`              // "en-US"
	Timezone               string `json:"timezone"`                 // "America/Los_Angeles"
	BetaVariantID          string `json:"beta_variant_id"`          // pool key, e.g. "full"
	TLSProfileID           int64  `json:"tls_profile_id"`           // 0 means "use builtin"
	CacheBreakpointVariant string `json:"cache_breakpoint_variant"` // "default"/"single"/...

	// --- bookkeeping ---
	PoolID        string `json:"pool_id"`        // pool checksum at generation time
	SchemaVersion int    `json:"schema_version"` // matches package SchemaVersion
	UpdatedAt     int64  `json:"updated_at"`     // unix seconds
}

// SupportedOS lists the OS families the package knows how to validate.
// Anything outside this list is treated as a configuration error by Validate.
var SupportedOS = []string{"MacOS", "Linux", "Windows"}

// SupportedArch lists the CPU architectures the package supports.
var SupportedArch = []string{"arm64", "x64"}

// BuildUserAgent returns the User-Agent string Claude Code emits for the
// given CLI version. Format observed on the wire: "claude-cli/X.Y.Z (external, cli)".
//
// Kept as a function rather than a string template so the (external, cli)
// suffix can evolve without ripple changes through Persona consumers.
func BuildUserAgent(cliVersion string) string {
	return "claude-cli/" + cliVersion + " (external, cli)"
}

// Validate enforces the internal-consistency constraints that the selection
// algorithm guarantees on output. Mostly useful as a regression check on
// hand-crafted Personas (e.g. fixtures) and on round-tripped Personas loaded
// from cache that may predate a schema migration.
func (p Persona) Validate() error {
	if err := validateOS(p.OS); err != nil {
		return err
	}
	if err := validateArch(p.Arch); err != nil {
		return err
	}
	if err := validateOSArchCompatibility(p.OS, p.Arch); err != nil {
		return err
	}
	if err := validateCPUForArch(p.CPUModel, p.Arch); err != nil {
		return err
	}
	if err := validateKernelForOS(p.KernelVersion, p.OS); err != nil {
		return err
	}
	if p.CLIVersion == "" {
		return fmt.Errorf("persona: empty CLIVersion")
	}
	if p.UserAgent == "" {
		return fmt.Errorf("persona: empty UserAgent")
	}
	if expected := BuildUserAgent(p.CLIVersion); p.UserAgent != expected {
		return fmt.Errorf("persona: UserAgent %q does not match CLIVersion %q (expected %q)",
			p.UserAgent, p.CLIVersion, expected)
	}
	if p.ClientID == "" || len(p.ClientID) != 64 {
		return fmt.Errorf("persona: ClientID must be 64 hex chars, got len=%d", len(p.ClientID))
	}
	if p.StainlessOS != p.OS {
		return fmt.Errorf("persona: StainlessOS=%q must match OS=%q", p.StainlessOS, p.OS)
	}
	if p.StainlessArch != p.Arch {
		return fmt.Errorf("persona: StainlessArch=%q must match Arch=%q", p.StainlessArch, p.Arch)
	}
	if p.BetaVariantID == "" {
		return fmt.Errorf("persona: empty BetaVariantID")
	}
	if p.CacheBreakpointVariant == "" {
		return fmt.Errorf("persona: empty CacheBreakpointVariant")
	}
	if p.SchemaVersion == 0 {
		return fmt.Errorf("persona: SchemaVersion must be set")
	}
	return nil
}

// --- validation helpers ---

func validateOS(os string) error {
	for _, supported := range SupportedOS {
		if os == supported {
			return nil
		}
	}
	return fmt.Errorf("persona: unsupported OS %q (supported: %v)", os, SupportedOS)
}

func validateArch(arch string) error {
	for _, supported := range SupportedArch {
		if arch == supported {
			return nil
		}
	}
	return fmt.Errorf("persona: unsupported Arch %q (supported: %v)", arch, SupportedArch)
}

// validateOSArchCompatibility — Windows on arm64 exists in the wild (Surface
// Pro X) but Claude Code historically targets x64 on Windows. We allow both
// to keep doors open but document the expectation here.
func validateOSArchCompatibility(os, arch string) error {
	// No hard rejections today — every (OS, Arch) in SupportedOS × SupportedArch
	// has a plausible real-world combination. The CPU/Kernel constraints below
	// catch the cases that really matter.
	_ = os
	_ = arch
	return nil
}

// validateCPUForArch enforces "Apple silicon → arm64, Intel/AMD → x64".
// We don't try to validate full CPU model strings — just catch obvious cross-
// arch nonsense.
func validateCPUForArch(cpu, arch string) error {
	if cpu == "" {
		return fmt.Errorf("persona: empty CPUModel")
	}
	lower := strings.ToLower(cpu)
	isApple := strings.HasPrefix(lower, "apple ")
	switch arch {
	case "arm64":
		// Apple silicon is the dominant arm64 desktop. Non-Apple arm64 (Surface
		// Pro X "Microsoft SQ", Ampere Altra) is rare enough that we don't try
		// to whitelist names here — but we reject obvious Intel/AMD on arm64.
		if strings.Contains(lower, "intel") || strings.Contains(lower, "amd ryzen") || strings.Contains(lower, "amd epyc") {
			return fmt.Errorf("persona: x64 CPU %q on arm64", cpu)
		}
	case "x64":
		if isApple {
			return fmt.Errorf("persona: Apple silicon %q on x64 (Apple M-series is arm64-only)", cpu)
		}
	}
	return nil
}

// validateKernelForOS does a soft sanity check: macOS Darwin kernel versions
// are 22.x – 25.x in the realistic window; Linux kernels are 5.x – 6.x;
// Windows reports "10.0.X". Wildly mismatched values likely indicate a pool
// misconfiguration.
func validateKernelForOS(kernel, os string) error {
	if kernel == "" {
		return fmt.Errorf("persona: empty KernelVersion")
	}
	switch os {
	case "MacOS":
		// Expect e.g. "23.6.0" / "24.1.0"
		if !looksLikeDarwinKernel(kernel) {
			return fmt.Errorf("persona: KernelVersion %q does not look like a Darwin kernel (e.g. 23.6.0)", kernel)
		}
	case "Linux":
		// Expect e.g. "6.5.0-15-generic"
		if !looksLikeLinuxKernel(kernel) {
			return fmt.Errorf("persona: KernelVersion %q does not look like a Linux kernel (e.g. 6.5.0-15-generic)", kernel)
		}
	case "Windows":
		// Expect e.g. "10.0.22631" or "10.0.26100"
		if !strings.HasPrefix(kernel, "10.0.") {
			return fmt.Errorf("persona: KernelVersion %q does not look like Windows NT 10.0.X", kernel)
		}
	}
	return nil
}

func looksLikeDarwinKernel(s string) bool {
	// "23.6.0" — three dot-separated numeric components, first >= 20.
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return false
	}
	for _, p := range parts {
		if p == "" {
			return false
		}
		for _, r := range p {
			if r < '0' || r > '9' {
				return false
			}
		}
	}
	// First component should be at least 20 (Darwin 20 = macOS 11 Big Sur).
	first := parts[0]
	if len(first) == 1 || first[0] < '2' {
		return false
	}
	return true
}

func looksLikeLinuxKernel(s string) bool {
	// "6.5.0-15-generic" or "5.15.0-105-generic" — at least one dot, starts with digit.
	if len(s) < 3 || s[0] < '0' || s[0] > '9' {
		return false
	}
	if !strings.Contains(s, ".") {
		return false
	}
	return true
}
