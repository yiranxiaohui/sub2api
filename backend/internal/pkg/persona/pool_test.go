package persona

import (
	"strings"
	"testing"
)

func TestDefaultPool_ValidatesCleanly(t *testing.T) {
	pool := DefaultPool()
	if err := pool.Validate(); err != nil {
		t.Fatalf("DefaultPool() failed Validate: %v", err)
	}
	if pool.ID == "" {
		t.Error("Validate did not stamp Pool.ID")
	}
}

func TestPoolValidate_StampedIDIsDeterministic(t *testing.T) {
	a := DefaultPool()
	b := DefaultPool()
	if err := a.Validate(); err != nil {
		t.Fatalf("a.Validate: %v", err)
	}
	if err := b.Validate(); err != nil {
		t.Fatalf("b.Validate: %v", err)
	}
	if a.ID != b.ID {
		t.Errorf("two DefaultPool().Validate() runs produced different IDs: %q vs %q", a.ID, b.ID)
	}
}

func TestPoolValidate_DifferentContentDifferentID(t *testing.T) {
	a := DefaultPool()
	b := DefaultPool()
	// Bump a weight on b — should change ID.
	b.CLIVersions[0].Weight += 1
	if err := a.Validate(); err != nil {
		t.Fatal(err)
	}
	if err := b.Validate(); err != nil {
		t.Fatal(err)
	}
	if a.ID == b.ID {
		t.Error("changing a CLIVersion weight did not change Pool.ID")
	}
}

func TestPoolValidate_RejectsEmptyCLIVersions(t *testing.T) {
	pool := DefaultPool()
	pool.CLIVersions = nil
	if err := pool.Validate(); err == nil {
		t.Error("expected error for empty CLIVersions")
	}
}

func TestPoolValidate_RejectsEmptyOSCombos(t *testing.T) {
	pool := DefaultPool()
	pool.OSCombos = nil
	if err := pool.Validate(); err == nil {
		t.Error("expected error for empty OSCombos")
	}
}

func TestPoolValidate_RejectsZeroWeight(t *testing.T) {
	pool := DefaultPool()
	pool.CLIVersions[0].Weight = 0
	if err := pool.Validate(); err == nil {
		t.Error("expected error for zero weight")
	}
}

func TestPoolValidate_RejectsNegativeWeight(t *testing.T) {
	pool := DefaultPool()
	pool.CLIVersions[0].Weight = -5
	if err := pool.Validate(); err == nil {
		t.Error("expected error for negative weight")
	}
}

func TestOSComboValidate_RejectsAppleSiliconOnX64Pool(t *testing.T) {
	c := defaultMacOSX64Combo()
	c.CPUPool = append(c.CPUPool, "Apple M2")
	if err := c.Validate(); err == nil {
		t.Error("expected error for Apple silicon in x64 CPU pool")
	}
}

func TestOSComboValidate_RejectsIntelOnArm64Pool(t *testing.T) {
	c := defaultMacOSArm64Combo()
	c.CPUPool = append(c.CPUPool, "Intel Core i9-13900H")
	if err := c.Validate(); err == nil {
		t.Error("expected error for Intel CPU in arm64 pool")
	}
}

func TestOSComboValidate_RejectsLinuxKernelInMacOSPool(t *testing.T) {
	c := defaultMacOSArm64Combo()
	c.KernelPool = append(c.KernelPool, "6.5.0-15-generic")
	err := c.Validate()
	if err == nil {
		t.Error("expected error for Linux kernel in macOS pool")
	}
}

func TestOSComboValidate_RejectsEmptyKernelPool(t *testing.T) {
	c := defaultMacOSArm64Combo()
	c.KernelPool = nil
	if err := c.Validate(); err == nil {
		t.Error("expected error for empty KernelPool")
	}
}

func TestOSComboValidate_RejectsEmptyCPUPool(t *testing.T) {
	c := defaultMacOSArm64Combo()
	c.CPUPool = nil
	if err := c.Validate(); err == nil {
		t.Error("expected error for empty CPUPool")
	}
}

func TestOSComboValidate_RejectsEmptyNodeVersionPool(t *testing.T) {
	c := defaultMacOSArm64Combo()
	c.NodeVersionPool = nil
	if err := c.Validate(); err == nil {
		t.Error("expected error for empty NodeVersionPool")
	}
}

func TestOSComboValidate_RejectsEmptyHostnamePrefixPool(t *testing.T) {
	c := defaultMacOSArm64Combo()
	c.HostnamePrefixPool = nil
	if err := c.Validate(); err == nil {
		t.Error("expected error for empty HostnamePrefixPool")
	}
}

func TestPoolValidate_RejectsBetaVariantEmptyFlags(t *testing.T) {
	pool := DefaultPool()
	pool.BetaVariants[0].Flags = nil
	if err := pool.Validate(); err == nil {
		t.Error("expected error for empty Flags in BetaVariant")
	}
}

func TestPoolValidate_RejectsLocaleMissingTZ(t *testing.T) {
	pool := DefaultPool()
	pool.LocaleVariants[0].TZ = ""
	err := pool.Validate()
	if err == nil {
		t.Error("expected error for missing TZ")
	}
}

func TestDefaultPool_ContainsRealisticDistribution(t *testing.T) {
	// Smoke check that the curated defaults aren't pathologically skewed.
	pool := DefaultPool()
	if err := pool.Validate(); err != nil {
		t.Fatal(err)
	}

	// At least one of each major OS family should be present.
	families := map[string]bool{}
	for _, c := range pool.OSCombos {
		families[c.OS] = true
	}
	for _, fam := range []string{"MacOS", "Linux", "Windows"} {
		if !families[fam] {
			t.Errorf("default pool missing OS family %q — distribution looks artificial", fam)
		}
	}

	// CLI version pool should span at least 3 minor versions for a believable
	// long tail.
	versions := map[string]bool{}
	for _, v := range pool.CLIVersions {
		versions[v.Version] = true
	}
	if len(versions) < 3 {
		t.Errorf("default CLI version pool has only %d entries; fleets show more variety", len(versions))
	}

	// Pool.ID should be a 12-hex prefix + 'p'.
	if !strings.HasPrefix(pool.ID, "p") || len(pool.ID) != 13 {
		t.Errorf("Pool.ID should be 'p' + 12 hex chars, got %q", pool.ID)
	}
}

func TestPoolValidate_EmptyTLSProfileIDsAllowed(t *testing.T) {
	pool := DefaultPool()
	pool.TLSProfileIDs = nil
	if err := pool.Validate(); err != nil {
		t.Errorf("empty TLSProfileIDs should be allowed (means \"caller handles default\"): %v", err)
	}
}
