package persona

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

// SelectPersona deterministically derives a Persona for the given account
// from the supplied pool. Same (accountID, pool) → same Persona, forever.
//
// The pool MUST have been Validated; SelectPersona panics on a malformed pool
// (empty weighted slices, zero-weighted entries) because by the time we reach
// selection these are programmer errors that should have been caught at
// startup.
//
// Determinism is built on:
//
//  1. A 256-bit seed = SHA-256("sub2api-persona-v1::" + pool.ID + "::" + accountID).
//  2. The seed is split into independent 64-bit lanes; each axis (CLI version,
//     OS combo, beta variant, ...) draws from its own lane. This way changing
//     one axis weight in the pool doesn't ripple into every other axis.
//  3. Inside a lane, the index is computed by weighted modular arithmetic
//     (Walker-Alias would be slightly faster but the lookup table makes pool
//     hot-reload trickier; modular arithmetic is fine for our scale).
//
// updatedAt is taken from time.Now().Unix() at call time — it's the only
// non-deterministic field on the result, intentionally so we can detect stale
// Personas.
func SelectPersona(accountID int64, pool *Pool) (Persona, error) {
	if pool == nil {
		return Persona{}, fmt.Errorf("persona: nil pool")
	}
	if pool.ID == "" {
		return Persona{}, fmt.Errorf("persona: pool not validated (empty pool.ID); call Pool.Validate first")
	}

	seed := deriveSeed(accountID, pool.ID)
	lanes := splitSeedIntoLanes(seed)

	cli := pickCLIVersion(lanes[0], pool.CLIVersions)
	combo := pickOSCombo(lanes[1], pool.OSCombos)
	beta := pickBetaVariant(lanes[2], pool.BetaVariants)
	locale := pickLocale(lanes[3], pool.LocaleVariants)
	cacheBP := pickCacheBreakpoint(lanes[4], pool.CacheBreakpointVariants)

	// Sub-picks inside the chosen OSCombo. We reuse later lanes so the OS
	// pick (lanes[1]) doesn't directly determine which kernel/CPU shows up.
	kernel := pickString(lanes[5], combo.KernelPool)
	cpu := pickString(lanes[6], combo.CPUPool)
	nodeVer := pickString(lanes[7], combo.NodeVersionPool)
	hostnamePrefix := pickString(lanes[8], combo.HostnamePrefixPool)

	hostname := makeHostname(hostnamePrefix, lanes[9])
	clientID := makeClientID(seed) // full 256-bit → 64 hex
	tlsProfileID := pickTLSProfileID(lanes[10], pool.TLSProfileIDs)

	cliVer := cli.Version
	p := Persona{
		ClientID: clientID,

		OS:            combo.OS,
		Arch:          combo.Arch,
		KernelVersion: kernel,
		CPUModel:      cpu,
		Hostname:      hostname,

		CLIVersion:              cliVer,
		UserAgent:               BuildUserAgent(cliVer),
		StainlessLang:           "js",
		StainlessRuntime:        "node",
		StainlessRuntimeVersion: nodeVer,
		StainlessPackageVersion: cliVer,
		StainlessOS:             combo.OS,
		StainlessArch:           combo.Arch,

		LocaleLang:             locale.Lang,
		Timezone:               locale.TZ,
		BetaVariantID:          beta.ID,
		TLSProfileID:           tlsProfileID,
		CacheBreakpointVariant: cacheBP.ID,

		PoolID:        pool.ID,
		SchemaVersion: SchemaVersion,
		UpdatedAt:     time.Now().Unix(),
	}

	// Defense in depth: assert the consistency contract before returning.
	// Validate is cheap and a guard against pool bugs slipping through.
	if err := p.Validate(); err != nil {
		return Persona{}, fmt.Errorf("persona: SelectPersona produced invalid persona: %w", err)
	}
	return p, nil
}

// BetaFlagsFor returns the literal anthropic-beta flag list for the variant
// the persona was assigned. Callers that need to splice this into the
// outbound anthropic-beta header look it up by ID, since the Persona only
// carries the ID (smaller, easier to migrate).
func BetaFlagsFor(p Persona, pool *Pool) ([]string, bool) {
	for _, v := range pool.BetaVariants {
		if v.ID == p.BetaVariantID {
			out := make([]string, len(v.Flags))
			copy(out, v.Flags)
			return out, true
		}
	}
	return nil, false
}

// --- seed derivation ---

const seedSalt = "sub2api-persona-v1::"

// deriveSeed combines accountID with the pool checksum so changing the pool
// also changes every account's seed (predictably). Using SHA-256 makes the
// seed avalanche so neighbouring account IDs aren't visibly correlated.
func deriveSeed(accountID int64, poolID string) [32]byte {
	h := sha256.New()
	_, _ = h.Write([]byte(seedSalt))
	_, _ = h.Write([]byte(poolID))
	_, _ = h.Write([]byte("::"))
	var buf [8]byte
	binary.LittleEndian.PutUint64(buf[:], uint64(accountID))
	_, _ = h.Write(buf[:])
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}

// splitSeedIntoLanes carves the 256-bit seed into independent 64-bit lanes.
// We HMAC-style derive each lane from (seed, laneIndex) so the lanes are
// fully decorrelated — without this, picking with lanes[1] could be implicitly
// correlated with lanes[0].
func splitSeedIntoLanes(seed [32]byte) [16]uint64 {
	var out [16]uint64
	for i := range out {
		h := sha256.New()
		_, _ = h.Write(seed[:])
		_, _ = h.Write([]byte{byte(i)})
		sum := h.Sum(nil)
		out[i] = binary.LittleEndian.Uint64(sum[:8])
	}
	return out
}

// --- weighted picks ---

func pickCLIVersion(lane uint64, entries []CLIVersionEntry) CLIVersionEntry {
	if len(entries) == 0 {
		panic("persona: empty CLIVersions (pool not validated)")
	}
	total := uint64(0)
	for _, e := range entries {
		total += uint64(e.Weight)
	}
	pick := lane % total
	acc := uint64(0)
	for _, e := range entries {
		acc += uint64(e.Weight)
		if pick < acc {
			return e
		}
	}
	return entries[len(entries)-1] // unreachable
}

func pickOSCombo(lane uint64, combos []OSCombo) OSCombo {
	if len(combos) == 0 {
		panic("persona: empty OSCombos (pool not validated)")
	}
	total := uint64(0)
	for _, c := range combos {
		total += uint64(c.Weight)
	}
	pick := lane % total
	acc := uint64(0)
	for _, c := range combos {
		acc += uint64(c.Weight)
		if pick < acc {
			return c
		}
	}
	return combos[len(combos)-1]
}

func pickBetaVariant(lane uint64, variants []BetaVariant) BetaVariant {
	if len(variants) == 0 {
		panic("persona: empty BetaVariants (pool not validated)")
	}
	total := uint64(0)
	for _, v := range variants {
		total += uint64(v.Weight)
	}
	pick := lane % total
	acc := uint64(0)
	for _, v := range variants {
		acc += uint64(v.Weight)
		if pick < acc {
			return v
		}
	}
	return variants[len(variants)-1]
}

func pickLocale(lane uint64, variants []LocaleVariant) LocaleVariant {
	if len(variants) == 0 {
		panic("persona: empty LocaleVariants (pool not validated)")
	}
	total := uint64(0)
	for _, v := range variants {
		total += uint64(v.Weight)
	}
	pick := lane % total
	acc := uint64(0)
	for _, v := range variants {
		acc += uint64(v.Weight)
		if pick < acc {
			return v
		}
	}
	return variants[len(variants)-1]
}

func pickCacheBreakpoint(lane uint64, variants []CacheBreakpointVariant) CacheBreakpointVariant {
	if len(variants) == 0 {
		panic("persona: empty CacheBreakpointVariants (pool not validated)")
	}
	total := uint64(0)
	for _, v := range variants {
		total += uint64(v.Weight)
	}
	pick := lane % total
	acc := uint64(0)
	for _, v := range variants {
		acc += uint64(v.Weight)
		if pick < acc {
			return v
		}
	}
	return variants[len(variants)-1]
}

// pickString chooses an entry by simple modular index — sub-pools are not
// weighted internally.
func pickString(lane uint64, options []string) string {
	if len(options) == 0 {
		panic("persona: empty options (pool not validated)")
	}
	return options[lane%uint64(len(options))]
}

func pickTLSProfileID(lane uint64, ids []int64) int64 {
	if len(ids) == 0 {
		return 0
	}
	return ids[lane%uint64(len(ids))]
}

// --- assembly helpers ---

// makeHostname returns "{prefix}-{4hex}". Hostnames in the wild include a
// huge variety (defaults like "MacBook-Pro-2", user-named like "lisa-laptop",
// container/server-style like "ip-10-0-1-23"). Picking a prefix from a pool
// and tacking on 4 hex chars captures the structural variety without
// constructing a name that could collide with anything meaningful.
func makeHostname(prefix string, lane uint64) string {
	suffix := fmt.Sprintf("%04x", uint16(lane))
	if prefix == "" {
		return "host-" + suffix
	}
	// Windows prefixes are upper-case ("DESKTOP-XXXXXX") whereas Unix ones
	// are lower-case ("mbp-xxxx"); preserve whatever casing the pool used.
	if isUpper(prefix) {
		return prefix + "-" + strings.ToUpper(suffix)
	}
	return prefix + "-" + suffix
}

func isUpper(s string) bool {
	for _, r := range s {
		if r >= 'a' && r <= 'z' {
			return false
		}
	}
	return true
}

// makeClientID flattens the 256-bit seed to 64 hex characters — same format
// as the older Fingerprint.ClientID (crypto/rand sourced). Using the seed
// directly preserves determinism so the metadata.user_id "device_id" stays
// stable across cache flushes.
func makeClientID(seed [32]byte) string {
	return hex.EncodeToString(seed[:])
}
