package h2fingerprint

import (
	"net/http"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func TestOrderedHeaderKeys_EmptyHeader(t *testing.T) {
	got := OrderedHeaderKeys(http.Header{}, []string{"Accept", "User-Agent"})
	if got != nil {
		t.Errorf("expected nil for empty header, got %v", got)
	}
}

func TestOrderedHeaderKeys_AllInWireOrder(t *testing.T) {
	h := http.Header{
		"User-Agent":   {"claude-cli/2.1.81"},
		"Accept":       {"*/*"},
		"Content-Type": {"application/json"},
	}
	wire := []string{"Accept", "User-Agent", "content-type"}

	got := OrderedHeaderKeys(h, wire)
	want := []string{"Accept", "User-Agent", "content-type"}

	if !reflect.DeepEqual(got, want) {
		t.Errorf("OrderedHeaderKeys = %v, want %v", got, want)
	}
}

func TestOrderedHeaderKeys_WireCasingWins(t *testing.T) {
	// Even if the header was stored under a different casing (e.g. via Go's
	// canonical-case http.Header.Set), the wire order spec's casing wins.
	h := http.Header{
		"Content-Type": {"application/json"}, // stored canonical
	}
	wire := []string{"content-type"} // wire wants lowercase

	got := OrderedHeaderKeys(h, wire)
	want := []string{"content-type"}

	if !reflect.DeepEqual(got, want) {
		t.Errorf("OrderedHeaderKeys = %v, want %v", got, want)
	}
}

func TestOrderedHeaderKeys_ExtraKeysAppended(t *testing.T) {
	h := http.Header{
		"Accept":       {"*/*"},
		"X-Custom":     {"v1"}, // not in wire order
		"User-Agent":   {"u"},
		"X-Another":    {"v2"}, // not in wire order
		"Content-Type": {"json"},
	}
	wire := []string{"Accept", "User-Agent"}

	got := OrderedHeaderKeys(h, wire)

	// 2 wire-ordered keys + 3 non-wire keys = 5 total.
	if len(got) != 5 {
		t.Fatalf("expected 5 keys, got %d: %v", len(got), got)
	}
	if got[0] != "Accept" || got[1] != "User-Agent" {
		t.Errorf("wire-ordered prefix wrong: got %v", got[:2])
	}

	// Remaining three must be the non-wire keys, in some order
	// (map iteration is non-deterministic).
	rest := append([]string(nil), got[2:]...)
	sort.Strings(rest)
	wantRest := []string{"Content-Type", "X-Another", "X-Custom"}
	sort.Strings(wantRest)
	if !reflect.DeepEqual(rest, wantRest) {
		t.Errorf("trailing keys mismatch: got %v, want %v (any order)", rest, wantRest)
	}
}

func TestOrderedHeaderKeys_DedupesWireDuplicates(t *testing.T) {
	h := http.Header{
		"Accept": {"*/*"},
	}
	wire := []string{"Accept", "Accept", "accept"}

	got := OrderedHeaderKeys(h, wire)
	want := []string{"Accept"}

	if !reflect.DeepEqual(got, want) {
		t.Errorf("OrderedHeaderKeys = %v, want %v", got, want)
	}
}

func TestOrderedHeaderKeys_WireKeyMissingFromHeader(t *testing.T) {
	// Keys in wireOrder that aren't in h must not appear in the result.
	h := http.Header{
		"Accept": {"*/*"},
	}
	wire := []string{"Accept", "User-Agent", "Content-Type"}

	got := OrderedHeaderKeys(h, wire)
	want := []string{"Accept"}

	if !reflect.DeepEqual(got, want) {
		t.Errorf("OrderedHeaderKeys = %v, want %v", got, want)
	}
}

func TestOrderedHeaderKeys_PreservesAllHeaders(t *testing.T) {
	// Every key in h must appear exactly once in the result.
	h := http.Header{
		"A": {"1"},
		"B": {"2"},
		"C": {"3"},
		"D": {"4"},
		"E": {"5"},
	}
	wire := []string{"B", "D"}

	got := OrderedHeaderKeys(h, wire)

	if len(got) != 5 {
		t.Fatalf("expected 5 keys, got %d: %v", len(got), got)
	}
	if got[0] != "B" || got[1] != "D" {
		t.Errorf("wire-ordered prefix wrong: got %v", got[:2])
	}

	seen := map[string]int{}
	for _, k := range got {
		seen[strings.ToUpper(k)]++
	}
	for _, k := range []string{"A", "B", "C", "D", "E"} {
		if seen[k] != 1 {
			t.Errorf("key %q appeared %d times, want 1", k, seen[k])
		}
	}
}

func TestDefaults_NonEmpty(t *testing.T) {
	d := Defaults()
	if d.Name == "" {
		t.Error("Defaults().Name is empty")
	}
	if len(d.Settings) == 0 {
		t.Error("Defaults().Settings is empty")
	}
	if d.ConnectionFlow == 0 {
		t.Error("Defaults().ConnectionFlow is zero")
	}
	if len(d.PseudoHeaderOrder) == 0 {
		t.Error("Defaults().PseudoHeaderOrder is empty")
	}
	if len(d.HeaderOrder) == 0 {
		t.Error("Defaults().HeaderOrder is empty")
	}
}

func TestDefaults_MatchesNodeJSBaseline(t *testing.T) {
	d := Defaults()

	wantSettings := []Setting{
		{ID: SettingHeaderTableSize, Value: 65536},
		{ID: SettingEnablePush, Value: 0},
		{ID: SettingInitialWindowSize, Value: 6291456},
		{ID: SettingMaxHeaderListSize, Value: 65535},
	}
	if !reflect.DeepEqual(d.Settings, wantSettings) {
		t.Errorf("Settings = %+v, want %+v", d.Settings, wantSettings)
	}

	if d.ConnectionFlow != 15663105 {
		t.Errorf("ConnectionFlow = %d, want 15663105", d.ConnectionFlow)
	}

	wantPseudo := []string{":method", ":path", ":authority", ":scheme"}
	if !reflect.DeepEqual(d.PseudoHeaderOrder, wantPseudo) {
		t.Errorf("PseudoHeaderOrder = %v, want %v", d.PseudoHeaderOrder, wantPseudo)
	}

	if d.EmitHeaderPriority {
		t.Error("EmitHeaderPriority should default to false (Node.js does not emit PRIORITY)")
	}
}

func TestDefaults_ReturnsCopy(t *testing.T) {
	d1 := Defaults()
	d1.Settings[0].Value = 99999
	d1.PseudoHeaderOrder[0] = "MUTATED"
	d1.HeaderOrder[0] = "MUTATED"

	d2 := Defaults()
	if d2.Settings[0].Value == 99999 {
		t.Error("mutating d1.Settings leaked into Defaults()")
	}
	if d2.PseudoHeaderOrder[0] == "MUTATED" {
		t.Error("mutating d1.PseudoHeaderOrder leaked into Defaults()")
	}
	if d2.HeaderOrder[0] == "MUTATED" {
		t.Error("mutating d1.HeaderOrder leaked into Defaults()")
	}
}

func TestResolved_FillsZeroFields(t *testing.T) {
	p := H2Profile{}
	r := p.Resolved()

	d := Defaults()
	if r.Name != d.Name {
		t.Errorf("Name not filled: got %q", r.Name)
	}
	if !reflect.DeepEqual(r.Settings, d.Settings) {
		t.Errorf("Settings not filled: got %+v", r.Settings)
	}
	if r.ConnectionFlow != d.ConnectionFlow {
		t.Errorf("ConnectionFlow not filled: got %d", r.ConnectionFlow)
	}
	if !reflect.DeepEqual(r.PseudoHeaderOrder, d.PseudoHeaderOrder) {
		t.Errorf("PseudoHeaderOrder not filled: got %v", r.PseudoHeaderOrder)
	}
	if !reflect.DeepEqual(r.HeaderOrder, d.HeaderOrder) {
		t.Errorf("HeaderOrder not filled: got %v", r.HeaderOrder)
	}
}

func TestResolved_PreservesOverrides(t *testing.T) {
	p := H2Profile{
		Name:              "custom",
		ConnectionFlow:    12345,
		PseudoHeaderOrder: []string{":scheme", ":method", ":path", ":authority"},
		Settings:          []Setting{{ID: SettingMaxFrameSize, Value: 16384}},
		HeaderOrder:       []string{"X-Custom"},
	}
	r := p.Resolved()

	if r.Name != "custom" {
		t.Errorf("Name overridden: got %q", r.Name)
	}
	if r.ConnectionFlow != 12345 {
		t.Errorf("ConnectionFlow overridden: got %d", r.ConnectionFlow)
	}
	if len(r.Settings) != 1 || r.Settings[0].ID != SettingMaxFrameSize {
		t.Errorf("Settings overridden: got %+v", r.Settings)
	}
}
