package h2fingerprint

import (
	"net/http"
	"strings"
)

// OrderedHeaderKeys returns the keys present in h, sorted to match wireOrder
// as closely as possible.
//
// Rules:
//  1. Keys whose lowercase form appears in wireOrder are emitted first, in
//     wireOrder's sequence. The exact wire casing from wireOrder is preserved
//     in the result, even if h stored the value under a different casing.
//  2. Keys present in h but not in wireOrder are appended afterwards, in the
//     order they appear in h (which is map-iteration order — non-deterministic
//     but stable within a single call). Their casing is preserved as stored.
//  3. Each key is emitted exactly once. Duplicates in wireOrder are ignored.
//
// Matching is case-insensitive on the header name, since http.Header keys are
// canonicalized to title-case by stdlib but real-world wire formats use
// arbitrary casing (e.g. "x-app" lowercase).
//
// This helper is intentionally stdlib-only so it can be unit-tested without
// any HTTP client library installed.
func OrderedHeaderKeys(h http.Header, wireOrder []string) []string {
	if len(h) == 0 {
		return nil
	}

	// Build a lookup of lowercase header name → original key as stored in h.
	// http.Header is map[string][]string; multiple keys differing only in
	// case shouldn't happen via stdlib (which canonicalizes), but callers
	// using header_util.setHeaderRaw insert keys verbatim — we tolerate both.
	stored := make(map[string]string, len(h))
	for k := range h {
		lk := strings.ToLower(k)
		if _, ok := stored[lk]; !ok {
			stored[lk] = k
		}
	}

	out := make([]string, 0, len(h))
	emitted := make(map[string]struct{}, len(h))

	// Pass 1: wire order
	for _, wireKey := range wireOrder {
		lk := strings.ToLower(wireKey)
		if _, dup := emitted[lk]; dup {
			continue
		}
		if _, ok := stored[lk]; ok {
			// Prefer the wire casing over whatever was stored in h.
			out = append(out, wireKey)
			emitted[lk] = struct{}{}
		}
	}

	// Pass 2: remaining keys from h, in map-iteration order.
	// Map iteration order is randomized, which slightly weakens the
	// fingerprint for headers we didn't anticipate — that is unavoidable
	// without a richer order spec. Callers should keep wireOrder
	// comprehensive.
	for k := range h {
		lk := strings.ToLower(k)
		if _, dup := emitted[lk]; dup {
			continue
		}
		out = append(out, k)
		emitted[lk] = struct{}{}
	}

	return out
}
