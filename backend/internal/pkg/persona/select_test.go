package persona

import (
	"math"
	"strings"
	"testing"
)

func mustValidate(t *testing.T, p *Pool) {
	t.Helper()
	if err := p.Validate(); err != nil {
		t.Fatalf("pool validate: %v", err)
	}
}

func TestSelectPersona_RequiresValidatedPool(t *testing.T) {
	pool := DefaultPool() // not validated yet → pool.ID empty
	_, err := SelectPersona(1, pool)
	if err == nil {
		t.Error("expected error when pool not validated")
	}
	if err != nil && !strings.Contains(err.Error(), "Validate") {
		t.Errorf("error should hint that Validate is needed: %v", err)
	}
}

func TestSelectPersona_RejectsNilPool(t *testing.T) {
	_, err := SelectPersona(1, nil)
	if err == nil {
		t.Error("expected error for nil pool")
	}
}

func TestSelectPersona_Deterministic(t *testing.T) {
	pool := DefaultPool()
	mustValidate(t, pool)

	for _, accountID := range []int64{1, 42, 12345, 1<<31 - 1, 1 << 50} {
		p1, err := SelectPersona(accountID, pool)
		if err != nil {
			t.Fatalf("account %d: first call: %v", accountID, err)
		}
		p2, err := SelectPersona(accountID, pool)
		if err != nil {
			t.Fatalf("account %d: second call: %v", accountID, err)
		}
		// UpdatedAt may differ — zero it before comparing.
		p1.UpdatedAt = 0
		p2.UpdatedAt = 0
		if p1 != p2 {
			t.Errorf("account %d: SelectPersona is not deterministic across calls", accountID)
		}
	}
}

func TestSelectPersona_ValidatesOutput(t *testing.T) {
	pool := DefaultPool()
	mustValidate(t, pool)
	// Burn through many accounts to flush out any pool entry that produces
	// an inconsistent persona.
	for i := int64(0); i < 1000; i++ {
		p, err := SelectPersona(i, pool)
		if err != nil {
			t.Fatalf("account %d: %v", i, err)
		}
		if err := p.Validate(); err != nil {
			t.Fatalf("account %d: produced invalid persona: %v\npersona=%+v", i, err, p)
		}
	}
}

func TestSelectPersona_DifferentAccountsDifferentPersonas(t *testing.T) {
	pool := DefaultPool()
	mustValidate(t, pool)

	p1, _ := SelectPersona(1, pool)
	p2, _ := SelectPersona(2, pool)

	// They might *coincidentally* share some fields (OS, arch, etc.) but
	// the full tuple should differ for adjacent accounts.
	if p1.ClientID == p2.ClientID {
		t.Error("adjacent accounts produced identical ClientID — seed not mixing well")
	}
	if p1.Hostname == p2.Hostname {
		t.Error("adjacent accounts produced identical Hostname")
	}
}

func TestSelectPersona_PoolIDStamped(t *testing.T) {
	pool := DefaultPool()
	mustValidate(t, pool)

	p, err := SelectPersona(42, pool)
	if err != nil {
		t.Fatal(err)
	}
	if p.PoolID != pool.ID {
		t.Errorf("PoolID = %q, want %q", p.PoolID, pool.ID)
	}
}

func TestSelectPersona_DistributionMatchesWeights(t *testing.T) {
	// Run SelectPersona over a large account ID range, count how often each
	// OSCombo is chosen, and assert the distribution is within tolerance of
	// the configured weights. With 10000 samples and weights in the 5-35
	// range, the empirical share should land within ±5 percentage points of
	// the target — generous enough that the test isn't flaky but tight
	// enough to catch a broken weight implementation.
	pool := DefaultPool()
	mustValidate(t, pool)

	const N = 10000
	counts := map[string]int{}
	for i := int64(0); i < N; i++ {
		p, err := SelectPersona(i, pool)
		if err != nil {
			t.Fatal(err)
		}
		// Identify the chosen OSCombo by (OS, Arch).
		key := p.OS + "/" + p.Arch
		counts[key]++
	}

	totalWeight := 0
	for _, c := range pool.OSCombos {
		totalWeight += c.Weight
	}

	for _, c := range pool.OSCombos {
		key := c.OS + "/" + c.Arch
		observed := float64(counts[key]) / float64(N)
		expected := float64(c.Weight) / float64(totalWeight)
		if math.Abs(observed-expected) > 0.05 {
			t.Errorf("OSCombo %q: observed %.3f, expected %.3f (tolerance 0.05)",
				c.ID, observed, expected)
		}
	}
}

func TestSelectPersona_BetaDistribution(t *testing.T) {
	pool := DefaultPool()
	mustValidate(t, pool)

	const N = 10000
	counts := map[string]int{}
	for i := int64(0); i < N; i++ {
		p, err := SelectPersona(i, pool)
		if err != nil {
			t.Fatal(err)
		}
		counts[p.BetaVariantID]++
	}

	totalWeight := 0
	for _, v := range pool.BetaVariants {
		totalWeight += v.Weight
	}
	for _, v := range pool.BetaVariants {
		observed := float64(counts[v.ID]) / float64(N)
		expected := float64(v.Weight) / float64(totalWeight)
		if math.Abs(observed-expected) > 0.05 {
			t.Errorf("BetaVariant %q: observed %.3f, expected %.3f", v.ID, observed, expected)
		}
	}
}

func TestSelectPersona_AxesAreIndependent(t *testing.T) {
	// Picking OSCombo and CLIVersion are driven by different seed lanes, so
	// their joint distribution should look uncorrelated. We check this by
	// counting (osCombo, cliVersion) pairs and asserting the empirical joint
	// is close to the product of marginals.
	pool := DefaultPool()
	mustValidate(t, pool)

	const N = 20000
	osCount := map[string]int{}
	cliCount := map[string]int{}
	joint := map[[2]string]int{}
	for i := int64(0); i < N; i++ {
		p, _ := SelectPersona(i, pool)
		osKey := p.OS + "/" + p.Arch
		osCount[osKey]++
		cliCount[p.CLIVersion]++
		joint[[2]string{osKey, p.CLIVersion}]++
	}

	// For each (os, cli) pair, observed joint should be near (marginal_os * marginal_cli * N).
	// Tolerance is wider than the marginal check because joint samples are sparser.
	for pair, n := range joint {
		exp := float64(osCount[pair[0]]) * float64(cliCount[pair[1]]) / float64(N)
		obs := float64(n)
		// Allow ±2.5σ where σ ≈ sqrt(exp). Joint cells with exp < 50 are too
		// small to make a meaningful claim about — skip.
		if exp < 50 {
			continue
		}
		sigma := math.Sqrt(exp)
		if math.Abs(obs-exp) > 3*sigma {
			t.Errorf("joint count for %v: observed %v, expected ~%.1f (tolerance ±%.1f) — axes appear correlated",
				pair, obs, exp, 3*sigma)
		}
	}
}

func TestSelectPersona_CrossAccountCollision(t *testing.T) {
	// Across N accounts, how many share an identical (OS, Arch, CPU, Kernel,
	// CLIVersion, Beta, Hostname) tuple? Some sharing is unavoidable given
	// finite pool size; we just want to ensure no single tuple swallows a
	// dominant share — that would defeat the whole point.
	pool := DefaultPool()
	mustValidate(t, pool)

	const N = 5000
	tupleCount := map[string]int{}
	for i := int64(0); i < N; i++ {
		p, _ := SelectPersona(i, pool)
		key := strings.Join([]string{
			p.OS, p.Arch, p.CPUModel, p.KernelVersion, p.CLIVersion, p.BetaVariantID, p.Hostname,
		}, "|")
		tupleCount[key]++
	}

	// Hostname has 65k variants (4 hex chars) per prefix — collisions should
	// be extremely rare. Assert top bucket holds < 2% of accounts.
	maxBucket := 0
	for _, n := range tupleCount {
		if n > maxBucket {
			maxBucket = n
		}
	}
	if share := float64(maxBucket) / float64(N); share > 0.02 {
		t.Errorf("top collision bucket holds %d/%d accounts (%.2f%%) — pool granularity too low",
			maxBucket, N, share*100)
	}
}

func TestSelectPersona_PoolChangeMovesSomeAccounts(t *testing.T) {
	// If we change a single weight in the pool, MOST accounts should retain
	// their existing persona (the seed is derived from pool.ID which changes,
	// so technically ALL accounts get a new seed — but our axes are
	// independent and weight changes only affect cells near the threshold).
	//
	// In practice this test confirms: pool changes DO ripple to all
	// accounts (different pool.ID means different seed) but the structural
	// distribution is still well-mixed. We treat the pool ID changing as
	// the operational signal — admins should expect persona regeneration.
	a := DefaultPool()
	mustValidate(t, a)
	b := DefaultPool()
	b.CLIVersions[0].Weight += 1
	mustValidate(t, b)

	if a.ID == b.ID {
		t.Fatal("test setup: changing weight should change pool ID")
	}

	pa, _ := SelectPersona(42, a)
	pb, _ := SelectPersona(42, b)
	if pa.ClientID == pb.ClientID {
		t.Error("different pool ID should produce different ClientID for the same account")
	}
}

func TestBetaFlagsFor_HappyPath(t *testing.T) {
	pool := DefaultPool()
	mustValidate(t, pool)

	p, _ := SelectPersona(1, pool)
	flags, ok := BetaFlagsFor(p, pool)
	if !ok {
		t.Fatalf("variant %q not found in pool", p.BetaVariantID)
	}
	if len(flags) == 0 {
		t.Error("BetaFlagsFor returned empty flags")
	}
}

func TestBetaFlagsFor_UnknownVariant(t *testing.T) {
	pool := DefaultPool()
	mustValidate(t, pool)
	p := Persona{BetaVariantID: "does-not-exist"}
	_, ok := BetaFlagsFor(p, pool)
	if ok {
		t.Error("BetaFlagsFor should return ok=false for unknown variant")
	}
}

func TestBetaFlagsFor_ReturnsCopy(t *testing.T) {
	pool := DefaultPool()
	mustValidate(t, pool)
	p, _ := SelectPersona(1, pool)

	flags, _ := BetaFlagsFor(p, pool)
	original := append([]string(nil), flags...)
	flags[0] = "MUTATED"

	flags2, _ := BetaFlagsFor(p, pool)
	if flags2[0] != original[0] {
		t.Error("BetaFlagsFor returned a shared slice — caller mutation leaked back into the pool")
	}
}

func TestSelectPersona_HostnameFormatPerOS(t *testing.T) {
	pool := DefaultPool()
	mustValidate(t, pool)

	// Sample enough to hit each OS family.
	seen := map[string]int{}
	for i := int64(0); i < 2000; i++ {
		p, _ := SelectPersona(i, pool)
		seen[p.OS]++

		switch p.OS {
		case "Windows":
			// Windows hostnames should be all upper-case (DESKTOP-XXXX).
			if strings.ToUpper(p.Hostname) != p.Hostname {
				t.Errorf("Windows hostname not uppercase: %q", p.Hostname)
			}
		case "MacOS", "Linux":
			// Unix hostnames should be lowercase.
			if strings.ToLower(p.Hostname) != p.Hostname {
				t.Errorf("%s hostname not lowercase: %q", p.OS, p.Hostname)
			}
		}
	}
	// Make sure we actually exercised all OS families, otherwise the test
	// is silently passing.
	for _, fam := range []string{"MacOS", "Linux", "Windows"} {
		if seen[fam] == 0 {
			t.Errorf("test sampled 0 accounts for OS %q — coverage gap", fam)
		}
	}
}

func TestSelectPersona_ZeroAccountID(t *testing.T) {
	pool := DefaultPool()
	mustValidate(t, pool)
	// Should not crash and should still produce a valid persona.
	p, err := SelectPersona(0, pool)
	if err != nil {
		t.Fatalf("account 0: %v", err)
	}
	if err := p.Validate(); err != nil {
		t.Errorf("account 0 produced invalid persona: %v", err)
	}
}

func TestSelectPersona_NegativeAccountID(t *testing.T) {
	pool := DefaultPool()
	mustValidate(t, pool)
	// Some account ID schemes use negative IDs for system accounts. Should still work.
	p, err := SelectPersona(-1, pool)
	if err != nil {
		t.Fatalf("negative account: %v", err)
	}
	if err := p.Validate(); err != nil {
		t.Errorf("negative account produced invalid persona: %v", err)
	}
}

func TestSelectPersona_TLSProfileAssignment(t *testing.T) {
	pool := DefaultPool()
	pool.TLSProfileIDs = []int64{10, 20, 30, 40}
	mustValidate(t, pool)

	// All assigned TLSProfileIDs should be from the pool, and the
	// distribution should hit each one.
	seen := map[int64]bool{}
	for i := int64(0); i < 1000; i++ {
		p, _ := SelectPersona(i, pool)
		if p.TLSProfileID == 0 {
			t.Fatalf("account %d: TLSProfileID is 0 but pool is non-empty", i)
		}
		found := false
		for _, id := range pool.TLSProfileIDs {
			if id == p.TLSProfileID {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("account %d: TLSProfileID %d not in pool", i, p.TLSProfileID)
		}
		seen[p.TLSProfileID] = true
	}
	if len(seen) != len(pool.TLSProfileIDs) {
		t.Errorf("not all TLSProfileIDs were assigned over 1000 accounts: seen=%v", seen)
	}
}

func TestSelectPersona_EmptyTLSProfileIDsLeavesZero(t *testing.T) {
	pool := DefaultPool()
	pool.TLSProfileIDs = nil
	mustValidate(t, pool)

	for i := int64(0); i < 10; i++ {
		p, _ := SelectPersona(i, pool)
		if p.TLSProfileID != 0 {
			t.Errorf("account %d: TLSProfileID = %d, expected 0 when pool empty", i, p.TLSProfileID)
		}
	}
}
