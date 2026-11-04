package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func newAnthropicOAuthAccountWithMapping(accountType string, mapping map[string]any) *Account {
	credentials := map[string]any{"access_token": "test"}
	if mapping != nil {
		credentials["model_mapping"] = mapping
	}
	return &Account{Platform: PlatformAnthropic, Type: accountType, Credentials: credentials}
}

func TestGatewayService_isModelSupportedByAccount_AnthropicOAuthWhitelist(t *testing.T) {
	svc := &GatewayService{}

	for _, accountType := range []string{AccountTypeOAuth, AccountTypeSetupToken} {
		t.Run(accountType, func(t *testing.T) {
			unrestricted := newAnthropicOAuthAccountWithMapping(accountType, nil)
			require.True(t, svc.isModelSupportedByAccount(unrestricted, "claude-opus-4-7"))

			longIDWhitelist := newAnthropicOAuthAccountWithMapping(accountType, map[string]any{
				"claude-sonnet-4-5-20250929": "claude-sonnet-4-5-20250929",
				"claude-opus-4-7":            "claude-opus-4-7",
			})
			require.True(t, svc.isModelSupportedByAccount(longIDWhitelist, "claude-opus-4-7"))
			require.True(t, svc.isModelSupportedByAccount(longIDWhitelist, "claude-sonnet-4-5"), "short ID should match a long-ID whitelist entry")
			require.True(t, svc.isModelSupportedByAccount(longIDWhitelist, "claude-sonnet-4-5-20250929"))
			require.False(t, svc.isModelSupportedByAccount(longIDWhitelist, "claude-opus-5"))

			shortIDWhitelist := newAnthropicOAuthAccountWithMapping(accountType, map[string]any{
				"claude-sonnet-4-5": "claude-sonnet-4-5",
			})
			require.True(t, svc.isModelSupportedByAccount(shortIDWhitelist, "claude-sonnet-4-5"), "short ID should match a short-ID whitelist entry")
			require.False(t, svc.isModelSupportedByAccount(shortIDWhitelist, "claude-haiku-4-5"))
		})
	}
}

func TestResolveAnthropicOAuthAccountMappedModel(t *testing.T) {
	tests := []struct {
		name      string
		account   *Account
		requested string
		want      string
		wantOK    bool
	}{
		{
			name:      "no mapping keeps request",
			account:   newAnthropicOAuthAccountWithMapping(AccountTypeOAuth, nil),
			requested: "claude-sonnet-4-5",
			want:      "claude-sonnet-4-5",
		},
		{
			name: "whitelist identity entry is not a rewrite",
			account: newAnthropicOAuthAccountWithMapping(AccountTypeOAuth, map[string]any{
				"claude-opus-4-7": "claude-opus-4-7",
			}),
			requested: "claude-opus-4-7",
			want:      "claude-opus-4-7",
		},
		{
			name: "long-ID whitelist entry for short request is not a rewrite",
			account: newAnthropicOAuthAccountWithMapping(AccountTypeOAuth, map[string]any{
				"claude-sonnet-4-5-20250929": "claude-sonnet-4-5-20250929",
			}),
			requested: "claude-sonnet-4-5",
			want:      "claude-sonnet-4-5",
		},
		{
			name: "explicit mapping rewrites and normalizes target",
			account: newAnthropicOAuthAccountWithMapping(AccountTypeSetupToken, map[string]any{
				"claude-opus-4-7": "claude-sonnet-4-5",
			}),
			requested: "claude-opus-4-7",
			want:      "claude-sonnet-4-5-20250929",
			wantOK:    true,
		},
		{
			name: "wildcard mapping applies",
			account: newAnthropicOAuthAccountWithMapping(AccountTypeOAuth, map[string]any{
				"claude-opus-*": "claude-opus-4-7",
			}),
			requested: "claude-opus-5",
			want:      "claude-opus-4-7",
			wantOK:    true,
		},
		{
			name: "mapping keyed by long ID applies to short request",
			account: newAnthropicOAuthAccountWithMapping(AccountTypeOAuth, map[string]any{
				"claude-haiku-4-5-20251001": "claude-sonnet-4-6",
			}),
			requested: "claude-haiku-4-5",
			want:      "claude-sonnet-4-6",
			wantOK:    true,
		},
		{
			name: "api key accounts are ignored",
			account: &Account{Platform: PlatformAnthropic, Type: AccountTypeAPIKey, Credentials: map[string]any{
				"model_mapping": map[string]any{"claude-opus-4-7": "claude-sonnet-4-6"},
			}},
			requested: "claude-opus-4-7",
			want:      "claude-opus-4-7",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := resolveAnthropicOAuthAccountMappedModel(tt.account, tt.requested)
			require.Equal(t, tt.wantOK, ok)
			require.Equal(t, tt.want, got)
		})
	}
}
