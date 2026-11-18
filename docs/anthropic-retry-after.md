# Anthropic 429 backpressure

A standard `Retry-After` response header is a server-requested temporary wait,
not evidence that an account is suspended. Previously the generic Anthropic 429
path could ignore it and use the local seconds-level fallback (5 seconds by
default), causing requests to resume earlier than the upstream requested.

## Behavior

- On ordinary Anthropic 429 responses, accept positive HTTP delay-seconds or a
  future HTTP-date. Reject malformed, fractional, negative, zero, expired and
  duration-overflow values; existing reset-header/fallback behavior remains for
  those inputs.
- A valid explicit wait takes precedence over local temporary-unschedulable
  keyword rules and the configurable fallback cooldown. Disabling that fallback
  does not disable an explicit upstream wait.
- Keep the later of the explicit wait, a demonstrably exhausted shared 5h/7d
  window, a valid aggregate reset and an already observed account cooldown.
  Use the production repository's atomic `SetRateLimitedIfLater` operation so
  concurrent stale account snapshots cannot overwrite a longer cooldown.
- A short `Retry-After` alone does not manufacture a 5h session window. Existing
  session bookkeeping is retained when exhausted shared windows provide a valid
  5h reset.
- Existing Fable model-only credit/window errors remain model-scoped. They do
  not turn into account-wide cooldowns just because `Retry-After` is present.
- No changes to pool-mode/custom-status-code opt-outs, other providers' retry
  policies, credentials, TLS, identities, proxy routing or production settings.

## Limits

This change prevents one class of premature retries. It does not implement a
universal request-rate policy, prove any relationship to provider account
suspension, or guarantee that a request sent before the 429 arrived can be
cancelled. Database persistence failures are logged and are not proof of a
durable scheduling pause. Existing administrator opt-outs remain in force.

No token, raw upstream body, proxy credential or account email is added to the
new log entries; only local account ID, reset time and persistence errors are
recorded.

## Tests

`ratelimit_service_anthropic_retry_after_test.go` covers parsing boundaries,
public error handling, precedence over local rules, disabled fallback behavior,
existing/shared-window bounds, atomic extension from a stale account snapshot,
model-only limit isolation and malformed-header fallback.

```sh
cd backend
go test -tags=unit ./internal/service -run 'AnthropicRetryAfter|Handle429|AnthropicWindowLimit'
go test -tags=unit ./...
go test -tags=integration ./...
```
