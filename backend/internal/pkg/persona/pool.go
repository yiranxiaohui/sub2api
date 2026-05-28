package persona

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

// Pool is the input to [SelectPersona]. It is the set of legal axis values
// (CLI versions, OS combos, beta variants, ...) plus the weights used to
// sample them.
//
// A Pool is meant to be loaded once at startup (from config / DB) and reused
// for every selection. It is safe for concurrent use after [Pool.Validate]
// has been called — Validate also computes [Pool.ID], the checksum stamped
// into each Persona so that "what pool was this Persona generated against"
// is recoverable later.
type Pool struct {
	// CLIVersions: weighted list of claude-cli versions. Order doesn't matter;
	// weights are normalized at selection time.
	CLIVersions []CLIVersionEntry

	// OSCombos: each entry bundles OS + Arch + kernel/CPU pools + the runtime
	// versions plausible on that platform. Picking an OSCombo entry locks in
	// all of these as a consistent set.
	OSCombos []OSCombo

	// BetaVariants: anthropic-beta flag bundles, named so debugging /
	// observability can group accounts.
	BetaVariants []BetaVariant

	// LocaleVariants: lang + timezone pairings. Should ideally be chosen to
	// match the proxy IP geo (out of scope for this package — selection here
	// is pure weighted, callers can post-filter).
	LocaleVariants []LocaleVariant

	// TLSProfileIDs: TLS fingerprint profile IDs to assign in rotation.
	// Empty means "leave TLSProfileID=0 (caller handles default)".
	TLSProfileIDs []int64

	// CacheBreakpointVariants: per-account prompt-cache breakpoint strategy.
	CacheBreakpointVariants []CacheBreakpointVariant

	// ID is filled in by Validate(); do not set by hand.
	ID string
}

// CLIVersionEntry is one row of Pool.CLIVersions.
type CLIVersionEntry struct {
	Version string
	Weight  int
}

// OSCombo bundles every machine-identity field that must vary as one unit.
//
// Why grouped: picking the OS independently from the kernel / CPU produces
// nonsense like "Windows with Darwin kernel 23.6.0". By making OSCombo the
// atomic unit, selection picks one combo, then sub-picks a CPU / kernel /
// runtime within it — every drawn Persona is internally consistent by
// construction.
type OSCombo struct {
	ID                 string   // identifier for telemetry, e.g. "macos_arm64"
	Weight             int      // weighted probability vs other OSCombos
	OS                 string   // matches Persona.OS
	Arch               string   // matches Persona.Arch
	KernelPool         []string // sub-pool of plausible kernel versions
	CPUPool            []string // sub-pool of plausible CPU models
	NodeVersionPool    []string // X-Stainless-Runtime-Version values
	HostnamePrefixPool []string // prefixes for generated hostnames ("mbp" / "ubuntu" / "win")
}

// BetaVariant is one row of Pool.BetaVariants. Flags is the literal list of
// anthropic-beta tokens emitted on the wire.
type BetaVariant struct {
	ID     string
	Weight int
	Flags  []string
}

// LocaleVariant is one row of Pool.LocaleVariants.
type LocaleVariant struct {
	Lang   string
	TZ     string
	Weight int
}

// CacheBreakpointVariant is one row of Pool.CacheBreakpointVariants.
type CacheBreakpointVariant struct {
	ID     string
	Weight int
}

// Validate enforces structural and consistency constraints on the pool, then
// computes and stamps Pool.ID. All variants must have positive weights, every
// OSCombo's sub-pools must be non-empty, and CPU/Arch and Kernel/OS pairings
// must satisfy the same constraints applied to assembled Personas.
//
// Validate must be called before SelectPersona; selection assumes a validated
// pool and will index out of bounds on misconfigured inputs.
func (p *Pool) Validate() error {
	if len(p.CLIVersions) == 0 {
		return fmt.Errorf("persona pool: CLIVersions is empty")
	}
	if err := validateWeightedCLIs(p.CLIVersions); err != nil {
		return err
	}

	if len(p.OSCombos) == 0 {
		return fmt.Errorf("persona pool: OSCombos is empty")
	}
	for i, combo := range p.OSCombos {
		if err := combo.Validate(); err != nil {
			return fmt.Errorf("persona pool: OSCombos[%d] (id=%q): %w", i, combo.ID, err)
		}
	}

	if len(p.BetaVariants) == 0 {
		return fmt.Errorf("persona pool: BetaVariants is empty")
	}
	for i, b := range p.BetaVariants {
		if b.ID == "" {
			return fmt.Errorf("persona pool: BetaVariants[%d] missing ID", i)
		}
		if b.Weight <= 0 {
			return fmt.Errorf("persona pool: BetaVariants[%d] (id=%q) weight must be positive", i, b.ID)
		}
		if len(b.Flags) == 0 {
			return fmt.Errorf("persona pool: BetaVariants[%d] (id=%q) has empty flags", i, b.ID)
		}
	}

	if len(p.LocaleVariants) == 0 {
		return fmt.Errorf("persona pool: LocaleVariants is empty")
	}
	for i, l := range p.LocaleVariants {
		if l.Lang == "" {
			return fmt.Errorf("persona pool: LocaleVariants[%d] missing Lang", i)
		}
		if l.TZ == "" {
			return fmt.Errorf("persona pool: LocaleVariants[%d] missing TZ", i)
		}
		if l.Weight <= 0 {
			return fmt.Errorf("persona pool: LocaleVariants[%d] weight must be positive", i)
		}
	}

	if len(p.CacheBreakpointVariants) == 0 {
		return fmt.Errorf("persona pool: CacheBreakpointVariants is empty")
	}
	for i, v := range p.CacheBreakpointVariants {
		if v.ID == "" {
			return fmt.Errorf("persona pool: CacheBreakpointVariants[%d] missing ID", i)
		}
		if v.Weight <= 0 {
			return fmt.Errorf("persona pool: CacheBreakpointVariants[%d] weight must be positive", i)
		}
	}

	// TLSProfileIDs may be empty — that signals "don't auto-assign".

	p.ID = computePoolID(p)
	return nil
}

// Validate checks one OSCombo entry's invariants. Called from Pool.Validate;
// also exported so callers can sanity-check hand-rolled combos.
func (c *OSCombo) Validate() error {
	if c.ID == "" {
		return fmt.Errorf("missing ID")
	}
	if c.Weight <= 0 {
		return fmt.Errorf("weight must be positive")
	}
	if err := validateOS(c.OS); err != nil {
		return err
	}
	if err := validateArch(c.Arch); err != nil {
		return err
	}
	if len(c.KernelPool) == 0 {
		return fmt.Errorf("KernelPool is empty")
	}
	for _, k := range c.KernelPool {
		if err := validateKernelForOS(k, c.OS); err != nil {
			return fmt.Errorf("KernelPool entry: %w", err)
		}
	}
	if len(c.CPUPool) == 0 {
		return fmt.Errorf("CPUPool is empty")
	}
	for _, cpu := range c.CPUPool {
		if err := validateCPUForArch(cpu, c.Arch); err != nil {
			return fmt.Errorf("CPUPool entry: %w", err)
		}
	}
	if len(c.NodeVersionPool) == 0 {
		return fmt.Errorf("NodeVersionPool is empty")
	}
	if len(c.HostnamePrefixPool) == 0 {
		return fmt.Errorf("HostnamePrefixPool is empty")
	}
	return nil
}

func validateWeightedCLIs(entries []CLIVersionEntry) error {
	for i, e := range entries {
		if e.Version == "" {
			return fmt.Errorf("persona pool: CLIVersions[%d] missing Version", i)
		}
		if e.Weight <= 0 {
			return fmt.Errorf("persona pool: CLIVersions[%d] (version=%q) weight must be positive", i, e.Version)
		}
	}
	return nil
}

// computePoolID returns a short stable checksum of the pool's content. Used
// to mark which pool a Persona was generated against — when the pool changes
// substantively, regenerated Personas can be detected without changing
// SchemaVersion.
func computePoolID(p *Pool) string {
	// Hash a stable JSON projection. We deliberately exclude Pool.ID itself
	// so the function is idempotent.
	tmp := struct {
		CLI       []CLIVersionEntry
		OS        []OSCombo
		Beta      []BetaVariant
		Locale    []LocaleVariant
		TLS       []int64
		BPVariant []CacheBreakpointVariant
	}{p.CLIVersions, p.OSCombos, p.BetaVariants, p.LocaleVariants, p.TLSProfileIDs, p.CacheBreakpointVariants}
	bytes, _ := json.Marshal(tmp)
	sum := sha256.Sum256(bytes)
	return "p" + hex.EncodeToString(sum[:6]) // 12 hex chars ≈ 48 bits
}
