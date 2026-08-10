package service

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/model"
)

// randomModeAccount builds an Anthropic OAuth account with the
// tls_fingerprint_profile_id = -1 ("random") mode enabled.
func randomModeAccount(id int64) *Account {
	return &Account{
		ID:       id,
		Platform: PlatformAnthropic,
		Type:     AccountTypeOAuth,
		Extra: map[string]any{
			"enable_tls_fingerprint":     true,
			"tls_fingerprint_profile_id": float64(-1),
		},
	}
}

func TestResolveTLSProfile_RandomMode_DeterministicPerAccount(t *testing.T) {
	svc := &TLSFingerprintProfileService{}
	svc.setLocalCache([]*model.TLSFingerprintProfile{
		{ID: 1, Name: "p1"},
		{ID: 2, Name: "p2"},
		{ID: 3, Name: "p3"},
	})

	acct := randomModeAccount(42)

	first := svc.ResolveTLSProfile(acct)
	if first == nil {
		t.Fatal("random mode should resolve a profile")
	}

	// Same account → same profile on every call (no per-request re-roll).
	for i := 0; i < 5; i++ {
		got := svc.ResolveTLSProfile(acct)
		if got.Name != first.Name {
			t.Fatalf("same account must resolve to the same profile on every call; got %q then %q", first.Name, got.Name)
		}
	}

	// Deterministic across service restarts (hash-based, not rand-based).
	svc2 := &TLSFingerprintProfileService{}
	svc2.setLocalCache([]*model.TLSFingerprintProfile{
		{ID: 1, Name: "p1"},
		{ID: 2, Name: "p2"},
		{ID: 3, Name: "p3"},
	})
	if again := svc2.ResolveTLSProfile(acct); again.Name != first.Name {
		t.Errorf("selection must be deterministic across restarts: got %q, want %q", again.Name, first.Name)
	}
}

func TestResolveTLSProfile_RandomMode_DisabledAccountReturnsNil(t *testing.T) {
	svc := &TLSFingerprintProfileService{}
	svc.setLocalCache([]*model.TLSFingerprintProfile{{ID: 1, Name: "p1"}})

	// TLS fingerprint not enabled → nil regardless of profile id.
	acct := &Account{
		ID:       7,
		Platform: PlatformAnthropic,
		Type:     AccountTypeOAuth,
		Extra: map[string]any{
			"tls_fingerprint_profile_id": float64(-1),
		},
	}
	if got := svc.ResolveTLSProfile(acct); got != nil {
		t.Errorf("expected nil for an account without enable_tls_fingerprint, got %v", got)
	}
}
