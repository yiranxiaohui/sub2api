# Claude CLI compatibility baseline

## Evidence and scope

The built-in baseline is a local capture of native Linux x64 Claude Code
`2.1.327` on 2026-11-18. The SDK headers were consistent across simple print
requests, interactive requests and a custom-base-URL probe:

| Header | Measured value |
| --- | --- |
| X-Stainless-Lang | js |
| X-Stainless-Package-Version | 0.128.0 |
| X-Stainless-OS | Linux |
| X-Stainless-Arch | x64 |
| X-Stainless-Runtime | node |
| X-Stainless-Runtime-Version | v26.12.1 |
| X-Stainless-Retry-Count | 0 |
| X-Stainless-Timeout | 600 |

`internal/pkg/claude/client_profile.go` is the single source for default SDK
headers and newly created fallback identity fields. The independently recorded
`testdata/native-linux-x64-2.1.327.json` fixture contains only non-secret header
values and two synthetic prompts with their measured billing suffixes. Raw
captures, credentials, account/organization IDs, device IDs and emails are not
checked into the repository.

This is a protocol-compatibility baseline, not proof of account-policy behavior,
not a claim of indistinguishability, and not a complete official-client emulator.
The host running the gateway does not determine this fallback profile's platform.
Existing cached identities are not migrated or changed to x64 by this change.
An administrator/runtime UA override still works; it does not imply that the
corresponding SDK, TLS or feature-flag profile has been measured. Capture and
review those values separately before treating a future version as verified.

## Forwarding behavior

- Do not recalculate an incoming billing suffix when its CLI version already
  matches the outgoing version. This preserves client-owned values and avoids
  hashing injected reminders or delta-only thread history in place of the
  original prompt.
- On an actual version change, billing extraction skips text blocks starting
  with `<system-reminder>` in the first user message. This is separate from
  `extractFirstUserText`, whose existing sticky-session semantics are unchanged.
- The gateway's generated requests no longer add the legacy
  `x-stainless-helper-method: stream`. Older clients that send it keep it on the
  forwarding path.
- Forward these optional headers when a client supplies them:
  `x-claude-1p-request-class`, `x-claude-code-request-class`,
  `x-claude-code-prompt-id`, `x-claude-code-sandy-plover`.
  They are not authentication or authorization signals and are not generated for
  requests that omit them.

## Deliberately unchanged

Native official-host traffic and custom-base-URL traffic are different. In the
observed custom-base-URL probe the CLI itself omitted `cch`, request-class headers,
`thread` and `diagnostics`; direct logged-in traffic included some of these.
The probe alone does not establish behavior for every account or configuration.

This change does not copy an account's feature flags, manufacture account
identity, enable `message-threads`, add optional beta capabilities, rewrite system
prompts, change cache TTL/pricing, implement CCH signing, change TLS profiles, or
change token-refresh behavior. It does not promise exact wire header order:
Go's HTTP transport still serializes the request.

Thread continuations can depend on server-side history and account affinity.
Preserving a same-version billing suffix does not implement thread routing or
cross-version migration; a version-changing continuation cannot generally be
reconstructed from its delta alone. Existing body transformations also do not
recompute a client's CCH value. These remain separate compatibility concerns.

## Verification

```sh
cd backend
go test ./internal/pkg/claude ./internal/service -run \
  'Test(DefaultHeadersCaptured|BillingCaptured|ExtractBilling|SyncBilling|DefaultFingerprint|GeneratedClientHeaders|BuildClientRequest|ClaudeCodeFingerprint|BuildOAuthRequest_Billing)'
go test -tags=unit ./...
go test -tags=integration ./...
```

The request-builder tests cover both `/v1/messages` and `/v1/messages/count_tokens`,
with optional headers present and absent. They use dummy credentials and do not
contact an external model service.
