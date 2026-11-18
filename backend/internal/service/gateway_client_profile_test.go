package service

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/claude"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestBillingCapturedPromptsIgnoreInjectedReminders(t *testing.T) {
	raw, err := os.ReadFile("../pkg/claude/testdata/native-linux-x64-2.1.327.json")
	require.NoError(t, err)
	var fixture struct {
		Capture struct {
			Version string `json:"cli_version"`
		} `json:"capture"`
		Cases []struct {
			Prompt string `json:"prompt"`
			Suffix string `json:"suffix"`
		} `json:"billing_cases"`
	}
	require.NoError(t, json.Unmarshal(raw, &fixture))
	for _, tc := range fixture.Cases {
		t.Run(tc.Prompt, func(t *testing.T) {
			const reminder = "<system-reminder>Test-only context, no account data.</system-reminder>"
			body, err := json.Marshal(map[string]any{
				"system": []map[string]string{{"type": "text", "text": "x-anthropic-billing-header: cc_version=2.1.100.abc; cc_entrypoint=sdk-cli;"}},
				"messages": []map[string]any{{"role": "user", "content": []map[string]string{
					{"type": "text", "text": reminder},
					{"type": "image"},
					{"type": "text", "text": tc.Prompt},
				}}},
			})
			require.NoError(t, err)
			require.Equal(t, tc.Suffix, computeClaudeCodeFingerprint(body, fixture.Capture.Version))
			require.Equal(t, reminder, extractFirstUserText(body), "sticky-session extraction must not change")
			result := syncBillingHeaderVersion(body, "claude-cli/"+fixture.Capture.Version+" (external, sdk-cli)")
			require.Equal(t, "x-anthropic-billing-header: cc_version="+fixture.Capture.Version+"."+tc.Suffix+"; cc_entrypoint=sdk-cli;", gjson.GetBytes(result, "system.0.text").String())
			require.Equal(t, gjson.GetBytes(body, "messages").Raw, gjson.GetBytes(result, "messages").Raw)
		})
	}
}

func TestExtractBillingUserTextBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name, body, want string
	}{
		{"string", `{"messages":[{"role":"user","content":"plain"}]}`, "plain"},
		{"string_reminder_is_user_text", `{"messages":[{"role":"user","content":"<system-reminder>literal</system-reminder>"}]}`, "<system-reminder>literal</system-reminder>"},
		{"only_reminder", `{"messages":[{"role":"user","content":[{"type":"text","text":"<system-reminder>context</system-reminder>"}]}]}`, ""},
		{"do_not_use_tool_output_or_later_turn", `{"messages":[{"role":"user","content":[{"type":"tool_result","content":"tool output"}]},{"role":"user","content":"later"}]}`, ""},
		{"assistant_then_user", `{"messages":[{"role":"assistant","content":"ignore"},{"role":"user","content":[{"type":"text","text":"first"},{"type":"text","text":"second"}]}]}`, "first"},
		{"empty_first_text", `{"messages":[{"role":"user","content":[{"type":"text","text":""},{"type":"text","text":"second"}]}]}`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, extractBillingUserText([]byte(tc.body)))
		})
	}
}

func TestSyncBillingSameVersionPreservesClientSuffixAndBytes(t *testing.T) {
	for _, body := range []string{
		`{ "system": [{"type":"text","text":"x-anthropic-billing-header: cc_version=2.1.327.502; cc_entrypoint=sdk-cli;"}], "messages": [{"role":"user","content":[{"type":"text","text":"<system-reminder>context</system-reminder>"},{"type":"text","text":"hi"}]}] }`,
		`{"system":[{"type":"text","text":"x-anthropic-billing-header: cc_version=2.1.327.abc; cc_entrypoint=cli; cch=12345;"}],"messages":[{"role":"user","content":"new turn without original prompt"}],"thread":{"type":"continue","previous_message_id":"msg_test"}}`,
	} {
		result := syncBillingHeaderVersion([]byte(body), "claude-cli/2.1.327 (external, cli)")
		require.Equal(t, body, string(result))
	}
}

func TestDefaultFingerprintSharesClientProfile(t *testing.T) {
	fp, p := defaultFingerprint(), claude.DefaultClientProfile()
	require.Equal(t, p.StainlessLang, fp.StainlessLang)
	require.Equal(t, p.StainlessPackageVersion, fp.StainlessPackageVersion)
	require.Equal(t, p.StainlessOS, fp.StainlessOS)
	require.Equal(t, p.StainlessArch, fp.StainlessArch)
	require.Equal(t, p.StainlessRuntime, fp.StainlessRuntime)
	require.Equal(t, p.StainlessRuntimeVersion, fp.StainlessRuntimeVersion)
	require.Empty(t, fp.ClientID, "never copy a capture's installation identity")
}

func TestGeneratedClientHeadersUseProfileWithoutStreamHelper(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	req.Header.Set("x-stainless-helper-method", "stream")
	applyClaudeCodeMimicHeaders(req, claude.DefaultUserAgent())
	for k, v := range claude.DefaultHeaders() {
		require.Equal(t, v, getHeaderRaw(req.Header, k), k)
	}
	require.Empty(t, getHeaderRaw(req.Header, "x-stainless-helper-method"))
	for _, k := range []string{"x-claude-code-prompt-id", "x-claude-1p-request-class", "x-claude-code-request-class", "x-claude-code-sandy-plover", "anthropic-beta"} {
		require.Empty(t, getHeaderRaw(req.Header, k), "must not invent optional capabilities: %s", k)
	}
}

func TestBuildClientRequestPreservesOptionalHeadersAndBilling(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, endpoint := range []string{"messages", "count_tokens"} {
		for _, supplied := range []bool{false, true} {
			name := endpoint + "/absent"
			if supplied {
				name = endpoint + "/supplied"
			}
			t.Run(name, func(t *testing.T) {
				c, _ := gin.CreateTestContext(httptest.NewRecorder())
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
				ua := "claude-cli/" + claude.CLICurrentVersion + " (external, sdk-cli)"
				c.Request.Header.Set("User-Agent", ua)
				c.Request.Header.Set("X-Stainless-Helper-Method", "stream")
				optional := map[string]string{
					"x-claude-1p-request-class": "main", "x-claude-code-request-class": "main",
					"x-claude-code-prompt-id": "00000000-0000-4000-8000-000000000001", "x-claude-code-sandy-plover": "[]",
				}
				if supplied {
					for k, v := range optional {
						c.Request.Header.Set(k, v)
					}
				}
				svc := &GatewayService{cfg: &config.Config{}, identityService: NewIdentityService(&stubIdentityCache{fingerprint: &Fingerprint{
					UserAgent: ua, ClientID: "test-installation", UpdatedAt: time.Now().Unix(),
				}})}
				body := []byte(`{"model":"claude-haiku-4-5","system":[{"type":"text","text":"x-anthropic-billing-header: cc_version=` + claude.CLICurrentVersion + `.502; cc_entrypoint=sdk-cli;"}],"messages":[{"role":"user","content":[{"type":"text","text":"<system-reminder>context</system-reminder>"},{"type":"text","text":"hi"}]}]}`)
				account := &Account{ID: 1, Platform: PlatformAnthropic, Type: AccountTypeOAuth}
				var req *http.Request
				var wire []byte
				var err error
				if endpoint == "messages" {
					req, wire, err = svc.buildUpstreamRequest(context.Background(), c, account, body, "test-token", "oauth", "claude-haiku-4-5", true, false)
				} else {
					req, wire, err = svc.buildCountTokensRequest(context.Background(), c, account, body, "test-token", "oauth", "claude-haiku-4-5", false)
				}
				require.NoError(t, err)
				defer func() { require.NoError(t, req.Body.Close()) }()
				require.Equal(t, body, wire)
				actual, err := io.ReadAll(req.Body)
				require.NoError(t, err)
				require.Equal(t, wire, actual)
				require.Equal(t, "stream", getHeaderRaw(req.Header, "x-stainless-helper-method"), "older real clients keep their header")
				for k, v := range optional {
					if supplied {
						require.Equal(t, []string{v}, req.Header[k], k)
					} else {
						require.Empty(t, getHeaderRaw(req.Header, k), k)
					}
				}
			})
		}
	}
}
