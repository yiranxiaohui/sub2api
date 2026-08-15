package service

import (
	"context"
	"fmt"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/model"
)

type memoryTLSFingerprintProfileRepository struct {
	profiles []*model.TLSFingerprintProfile
	nextID   int64
}

func (r *memoryTLSFingerprintProfileRepository) List(context.Context) ([]*model.TLSFingerprintProfile, error) {
	return append([]*model.TLSFingerprintProfile(nil), r.profiles...), nil
}

func (r *memoryTLSFingerprintProfileRepository) GetByID(_ context.Context, id int64) (*model.TLSFingerprintProfile, error) {
	for _, profile := range r.profiles {
		if profile.ID == id {
			return profile, nil
		}
	}
	return nil, nil
}

func (r *memoryTLSFingerprintProfileRepository) Create(_ context.Context, profile *model.TLSFingerprintProfile) (*model.TLSFingerprintProfile, error) {
	for _, existing := range r.profiles {
		if existing.Name == profile.Name {
			return nil, fmt.Errorf("duplicate profile %q", profile.Name)
		}
	}
	r.nextID++
	created := *profile
	created.ID = r.nextID
	r.profiles = append(r.profiles, &created)
	return &created, nil
}

func (r *memoryTLSFingerprintProfileRepository) Update(_ context.Context, profile *model.TLSFingerprintProfile) (*model.TLSFingerprintProfile, error) {
	return profile, nil
}

func (r *memoryTLSFingerprintProfileRepository) Delete(context.Context, int64) error {
	return nil
}

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

func TestGenerateRecommendedProfiles_Idempotent(t *testing.T) {
	repo := &memoryTLSFingerprintProfileRepository{}
	svc := &TLSFingerprintProfileService{
		repo:       repo,
		localCache: make(map[int64]*model.TLSFingerprintProfile),
	}

	first, err := svc.GenerateRecommendedProfiles(context.Background())
	if err != nil {
		t.Fatalf("first generation failed: %v", err)
	}
	if first.Created != 1 || len(first.Profiles) != 1 {
		t.Fatalf("first generation = created %d, profiles %d; want 1, 1", first.Created, len(first.Profiles))
	}
	generated := first.Profiles[0]
	if generated.Name != "Claude Code / Node.js 24.x" {
		t.Fatalf("generated profile name = %q", generated.Name)
	}
	if len(generated.CipherSuites) != 17 || len(generated.Extensions) != 14 {
		t.Fatalf("generated profile is incomplete: %d cipher suites, %d extensions", len(generated.CipherSuites), len(generated.Extensions))
	}
	if len(generated.ALPNProtocols) != 0 {
		t.Fatalf("generated ALPN must remain transport-aware, got %v", generated.ALPNProtocols)
	}

	second, err := svc.GenerateRecommendedProfiles(context.Background())
	if err != nil {
		t.Fatalf("second generation failed: %v", err)
	}
	if second.Created != 0 || len(second.Profiles) != 1 {
		t.Fatalf("second generation = created %d, profiles %d; want 0, 1", second.Created, len(second.Profiles))
	}
	if len(repo.profiles) != 1 {
		t.Fatalf("idempotent generation stored %d profiles; want 1", len(repo.profiles))
	}
}
