//go:build unit

package service

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestParseAnthropicRetryAfter(t *testing.T) {
	now := time.Date(2026, 11, 18, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		raw   string
		delay time.Duration
	}{
		{"120", 120 * time.Second}, {" 120 ", 120 * time.Second},
		{now.Add(time.Minute).Format(http.TimeFormat), time.Minute},
		{"", 0}, {"0", 0}, {"-1", 0}, {"+10", 0}, {"1.5", 0},
		{"NaN", 0}, {"Inf", 0}, {"1e6", 0}, {"9223372036854775807", 0},
		{"18446744073709551616", 0}, {"invalid", 0},
		{now.Add(-time.Second).Format(http.TimeFormat), 0},
	} {
		t.Run(tc.raw, func(t *testing.T) {
			h := http.Header{}
			h.Set("Retry-After", tc.raw)
			got := parseAnthropicRetryAfter(h, now)
			if tc.delay == 0 {
				require.Nil(t, got)
				return
			}
			require.NotNil(t, got)
			require.Equal(t, now.Add(tc.delay), *got)
		})
	}
}

func TestAnthropicRetryAfterPreemptsFallbackAndLocalRules(t *testing.T) {
	for _, fallbackEnabled := range []bool{true, false} {
		t.Run(strconv.FormatBool(fallbackEnabled), func(t *testing.T) {
			repo := &anthropicWindowLimitRepo{}
			settings := newMockSettingRepo()
			data, err := json.Marshal(RateLimit429CooldownSettings{Enabled: fallbackEnabled, CooldownSeconds: 5})
			require.NoError(t, err)
			settings.data[SettingKeyRateLimit429CooldownSettings] = string(data)
			svc := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
			svc.SetSettingService(NewSettingService(settings, &config.Config{}))
			account := &Account{ID: 42, Platform: PlatformAnthropic, Type: AccountTypeOAuth,
				Credentials: map[string]any{"temp_unschedulable_enabled": true, "temp_unschedulable_rules": []any{
					map[string]any{"error_code": float64(429), "keywords": []any{"rate limit"}, "duration_minutes": float64(1)},
				}}}
			h := http.Header{}
			h.Set("Retry-After", "120")
			before := time.Now()
			disabled := svc.HandleUpstreamError(context.Background(), account, 429, h, []byte(`{"error":{"type":"rate_limit_error","message":"rate limit"}}`))
			require.False(t, disabled)
			require.Equal(t, 1, repo.rateLimitCalls)
			require.Zero(t, repo.tempUnschedCalls)
			require.Zero(t, repo.sessionWindowCalls, "Retry-After does not imply a 5h session window")
			require.WithinDuration(t, before.Add(120*time.Second), repo.lastRateLimitReset, time.Second)
		})
	}
}

func TestAnthropicRetryAfterKeepsLaterBounds(t *testing.T) {
	for _, bound := range []string{"existing", "5h", "7d", "aggregate"} {
		t.Run(bound, func(t *testing.T) {
			reset := time.Now().Add(3 * time.Hour).Truncate(time.Second)
			repo := &anthropicWindowLimitRepo{}
			svc := NewRateLimitService(repo, nil, nil, nil, nil)
			account := &Account{ID: 42, Platform: PlatformAnthropic, Type: AccountTypeOAuth}
			h := http.Header{}
			h.Set("Retry-After", "60")
			switch bound {
			case "existing":
				account.RateLimitResetAt = &reset
			case "5h", "7d":
				h.Set("anthropic-ratelimit-unified-"+bound+"-status", "rejected")
				h.Set("anthropic-ratelimit-unified-"+bound+"-utilization", "1.0")
				h.Set("anthropic-ratelimit-unified-"+bound+"-reset", strconv.FormatInt(reset.Unix(), 10))
			case "aggregate":
				h.Set("anthropic-ratelimit-unified-reset", strconv.FormatInt(reset.Unix(), 10))
			}
			svc.HandleUpstreamError(context.Background(), account, 429, h, nil)
			require.Equal(t, reset, repo.lastRateLimitReset)
		})
	}
}

type anthropicRetryAfterAtomicRepo struct {
	anthropicWindowLimitRepo
	stored      time.Time
	atomicCalls int
}

func (r *anthropicRetryAfterAtomicRepo) SetRateLimitedIfLater(_ context.Context, _ int64, reset time.Time) error {
	r.atomicCalls++
	if reset.After(r.stored) {
		r.stored = reset
	}
	return nil
}

func TestAnthropicRetryAfterUsesAtomicExtension(t *testing.T) {
	stored := time.Now().Add(time.Hour)
	repo := &anthropicRetryAfterAtomicRepo{stored: stored}
	svc := NewRateLimitService(repo, nil, nil, nil, nil)
	account := &Account{ID: 42, Platform: PlatformAnthropic, Type: AccountTypeOAuth} // stale snapshot lacks reset
	h := http.Header{}
	h.Set("Retry-After", "60")
	svc.handle429(context.Background(), account, h, nil)
	require.Equal(t, 1, repo.atomicCalls)
	require.Zero(t, repo.rateLimitCalls)
	require.Equal(t, stored, repo.stored)
}

func TestAnthropicRetryAfterPreservesModelOnlyLimit(t *testing.T) {
	repo := &anthropicWindowLimitRepo{}
	svc := NewRateLimitService(repo, nil, nil, nil, nil)
	account := &Account{ID: 42, Platform: PlatformAnthropic, Type: AccountTypeOAuth}
	h := fable429Headers(time.Now().Add(time.Hour), time.Now().Add(24*time.Hour))
	h.Set("Retry-After", "120")
	svc.HandleUpstreamError(context.Background(), account, 429, h, nil)
	require.Zero(t, repo.rateLimitCalls)
	require.Equal(t, 1, repo.modelRateLimitCalls)
}

func TestAnthropicRetryAfterInvalidUsesFallback(t *testing.T) {
	repo := &anthropicWindowLimitRepo{}
	svc := NewRateLimitService(repo, nil, nil, nil, nil)
	account := &Account{ID: 42, Platform: PlatformAnthropic, Type: AccountTypeOAuth}
	h := http.Header{}
	h.Set("Retry-After", "NaN")
	before := time.Now()
	svc.handle429(context.Background(), account, h, nil)
	require.Equal(t, 1, repo.rateLimitCalls)
	require.WithinDuration(t, before.Add(5*time.Second), repo.lastRateLimitReset, time.Second)
}
