package service

import (
	"context"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// parseAnthropicRetryAfter accepts the HTTP delay-seconds or HTTP-date forms.
// Do not convert an unbounded integer through time.Duration: multiplication can
// overflow into an immediate retry. Invalid/past/zero hints use the existing
// reset-header/fallback behavior instead. Fractional seconds are not HTTP
// delay-seconds and are deliberately not accepted.
func parseAnthropicRetryAfter(headers http.Header, now time.Time) *time.Time {
	raw := strings.TrimSpace(headers.Get("Retry-After"))
	if raw == "" {
		return nil
	}
	if seconds, err := strconv.ParseUint(raw, 10, 64); err == nil && strings.Trim(raw, "0123456789") == "" {
		const maxDelaySeconds = uint64((1<<63 - 1) / int64(time.Second))
		if seconds == 0 || seconds > maxDelaySeconds {
			return nil
		}
		reset := now.Add(time.Duration(seconds) * time.Second)
		return &reset
	}
	if reset, err := http.ParseTime(raw); err == nil && reset.After(now) {
		return &reset
	}
	return nil
}

// handleAnthropicRetryAfter honors a server-requested delay even if the local
// fallback cooldown is disabled. This is a temporary scheduling pause, not an
// authentication error or evidence of an account suspension. Only callers that
// have ruled out model-only limits should use it.
func (s *RateLimitService) handleAnthropicRetryAfter(ctx context.Context, account *Account, headers http.Header) bool {
	if s == nil || s.accountRepo == nil || account == nil || account.Platform != PlatformAnthropic {
		return false
	}
	now := time.Now()
	reset := parseAnthropicRetryAfter(headers, now)
	if reset == nil {
		return false
	}
	// Shared quota windows are independent of a short per-minute retry hint.
	// Preserve the later bound without fabricating a 5h session window from it.
	limit := selectAnthropicExhaustedWindow(headers, now)
	if limit != nil && limit.resetAt.After(*reset) {
		reset = &limit.resetAt
	}
	if aggregate, ok := parseAnthropicResetTimestamp(headers.Get("anthropic-ratelimit-unified-reset"), now, 8*24*time.Hour); ok && aggregate.After(*reset) {
		reset = &aggregate
	}
	if account.RateLimitResetAt != nil && account.RateLimitResetAt.After(*reset) {
		reset = account.RateLimitResetAt
	}

	// Use the concrete repository's atomic max operation to avoid stale concurrent
	// responses overwriting a later cooldown. Repositories without the optional
	// operation retain the observed account's floor (principally test doubles).
	var err error
	if repo, ok := s.accountRepo.(interface {
		SetRateLimitedIfLater(context.Context, int64, time.Time) error
	}); ok {
		err = repo.SetRateLimitedIfLater(ctx, account.ID, *reset)
	} else {
		err = s.accountRepo.SetRateLimited(ctx, account.ID, *reset)
	}
	if err != nil {
		slog.Warn("anthropic_retry_after_persist_failed", "account_id", account.ID, "error", err)
	}
	// Preserve existing window bookkeeping only when an actual 5h reset was
	// supplied alongside proven shared-window exhaustion.
	if err == nil && limit != nil {
		if end, ok := parseAnthropicWindowReset(headers, "5h", now); ok {
			start := end.Add(-5 * time.Hour)
			if windowErr := s.accountRepo.UpdateSessionWindow(ctx, account.ID, &start, &end, "rejected"); windowErr != nil {
				slog.Warn("rate_limit_update_session_window_failed", "account_id", account.ID, "error", windowErr)
			}
		}
	}
	s.notifyAccountSchedulingBlocked(account, *reset, "anthropic_retry_after")
	slog.Info("anthropic_retry_after", "account_id", account.ID, "reset_at", *reset)
	return true
}
