//go:build unit

package service

import (
	"net/http"
	"os"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/domain"
	"github.com/Wei-Shaw/sub2api/internal/pkg/claude"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestValidateClaudeHaiku55Request(t *testing.T) {
	for _, tc := range []struct {
		name    string
		body    string
		wantErr string
	}{
		{"omitted thinking", `{}`, ""},
		{"adaptive", `{"thinking":{"type":"adaptive"},"output_config":{"effort":"max"}}`, ""},
		{"disabled at high", `{"thinking":{"type":"disabled"},"output_config":{"effort":"high"}}`, ""},
		{"disabled at default effort", `{"thinking":{"type":"disabled"}}`, ""},
		{"forced tool_choice any", `{"tool_choice":{"type":"any"}}`, ""},
		{"forced named tool", `{"tool_choice":{"type":"tool","name":"x"}}`, ""},
		{"default temperature", `{"temperature":1}`, ""},
		{"default top_p", `{"top_p":0.99}`, ""},
		{"manual budget", `{"thinking":{"type":"enabled","budget_tokens":2048}}`, "manual thinking budgets"},
		{"between_tools", `{"thinking":{"type":"between_tools"}}`, "between_tools"},
		{"disabled at xhigh", `{"thinking":{"type":"disabled"},"output_config":{"effort":"xhigh"}}`, "only at low, medium or high"},
		{"disabled at max", `{"thinking":{"type":"disabled"},"output_config":{"effort":"max"}}`, "only at low, medium or high"},
		{"custom temperature", `{"temperature":0.2}`, "temperature"},
		{"top_p 1", `{"top_p":1}`, "top_p"},
		{"temperature and top_p", `{"temperature":1,"top_p":0.99}`, "together"},
		{"top_k", `{"top_k":5}`, "top_k"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, model := range []string{"claude-haiku-5-5", "anthropic/claude-haiku-5.5", "global.anthropic.claude-haiku-5-5"} {
				err := validateClaude55Request([]byte(tc.body), model)
				if tc.wantErr == "" {
					require.NoError(t, err, model)
					continue
				}
				require.ErrorContains(t, err, tc.wantErr, model)
				require.ErrorContains(t, err, "claude-haiku-5-5", model)
			}
		})
	}
	// Haiku 4.5 keeps its permissive contract.
	require.NoError(t, validateClaude55Request([]byte(`{"thinking":{"type":"enabled","budget_tokens":2048},"temperature":0.2}`), "claude-haiku-4-5"))
}

func TestParseGatewayRequestHaiku55ThinkingDefaults(t *testing.T) {
	for _, tc := range []struct {
		body string
		want bool
	}{
		{`{"model":"claude-haiku-5-5","messages":[]}`, true},
		{`{"model":"claude-haiku-5-5","thinking":{"type":"adaptive"},"messages":[]}`, true},
		{`{"model":"claude-haiku-5-5","thinking":{"type":"disabled"},"messages":[]}`, false},
		{`{"model":"claude-haiku-4-5","messages":[]}`, false},
	} {
		parsed, err := ParseGatewayRequest(NewRequestBodyRef([]byte(tc.body)), domain.PlatformAnthropic)
		require.NoError(t, err)
		require.Equal(t, tc.want, parsed.ThinkingEnabled, tc.body)
	}
}

func TestFilterThinkingBlocksHaiku55(t *testing.T) {
	history := `"messages":[{"role":"user","content":"hi"},{"role":"assistant","content":[{"type":"thinking","thinking":"","signature":"sig"},{"type":"text","text":"hello"}]},{"role":"user","content":"again"}]`

	// Thinking is on by default, so signed history survives without a thinking field.
	kept := FilterThinkingBlocks([]byte(`{"model":"claude-haiku-5-5",`+history+`}`), "claude-haiku-5-5")
	require.Equal(t, "sig", gjson.GetBytes(kept, "messages.1.content.0.signature").String())

	// Explicitly disabled thinking strips the history blocks.
	stripped := FilterThinkingBlocks([]byte(`{"model":"claude-haiku-5-5","thinking":{"type":"disabled"},`+history+`}`), "claude-haiku-5-5")
	require.Equal(t, "text", gjson.GetBytes(stripped, "messages.1.content.0.type").String())
	require.Len(t, gjson.GetBytes(stripped, "messages.1.content").Array(), 1)
}

func TestClaudeOAuthBodyHaiku55SkipsTemperature(t *testing.T) {
	body := []byte(`{"model":"claude-haiku-5-5","top_p":0.99,"messages":[{"role":"user","content":"hi"}]}`)
	out, _ := normalizeClaudeOAuthRequestBody(body, "claude-haiku-5-5", claudeOAuthNormalizeOptions{})
	require.False(t, gjson.GetBytes(out, "temperature").Exists())

	older, _ := normalizeClaudeOAuthRequestBody([]byte(`{"model":"claude-haiku-4-5","messages":[{"role":"user","content":"hi"}]}`), "claude-haiku-4-5", claudeOAuthNormalizeOptions{})
	require.Equal(t, float64(1), gjson.GetBytes(older, "temperature").Float())
}

func TestHaiku55ToolsetDropsLegacyStreamingBeta(t *testing.T) {
	body := []byte(`{"model":"claude-haiku-5-5","tools":[{"type":"computer_toolset_20260801"}],"messages":[{"role":"user","content":"hi"}]}`)
	header := claude.BetaFineGrainedToolStreaming + "," + claude.BetaContext1M
	got := filterSonnet55ToolsetBeta(header, body, "claude-haiku-5-5")
	require.False(t, containsBetaToken(got, claude.BetaFineGrainedToolStreaming))
	require.True(t, containsBetaToken(got, claude.BetaContext1M))
	require.Equal(t, header, filterSonnet55ToolsetBeta(header, body, "claude-haiku-4-5"))

	svc := &GatewayService{cfg: &config.Config{Gateway: config.GatewayConfig{InjectBetaForAPIKey: true}}}
	headers := http.Header{}
	headers.Set("anthropic-beta", header)
	final, set := svc.computeFinalAnthropicBeta("apikey", false, "claude-haiku-5-5", headers, body, nil)
	require.True(t, set)
	require.False(t, containsBetaToken(final, claude.BetaFineGrainedToolStreaming))
}

func TestBedrockHaiku55(t *testing.T) {
	for _, region := range []string{"us-east-1", "eu-west-1", "ap-northeast-1"} {
		account := &Account{Platform: PlatformAnthropic, Type: AccountTypeBedrock, Credentials: map[string]any{"aws_region": region}}
		modelID, ok := ResolveBedrockModelID(account, "claude-haiku-5-5")
		require.True(t, ok, region)
		require.Equal(t, "global.anthropic.claude-haiku-5-5", modelID, region)
	}

	modelID := "global.anthropic.claude-haiku-5-5"
	got := sanitizeBedrockThinking([]byte(`{"thinking":{"type":"enabled","budget_tokens":4096}}`), modelID)
	require.Equal(t, "adaptive", gjson.GetBytes(got, "thinking.type").String())
	require.False(t, gjson.GetBytes(got, "thinking.budget_tokens").Exists())
	got = sanitizeBedrockThinking([]byte(`{"thinking":{"type":"disabled"}}`), modelID)
	require.Equal(t, "disabled", gjson.GetBytes(got, "thinking.type").String())

	input := []byte(`{"model":"claude-haiku-5-5","max_tokens":1024,"output_config":{"effort":"low"},"messages":[{"role":"user","content":"hello"}]}`)
	result, err := PrepareBedrockRequestBodyWithTokens(input, modelID, nil, false)
	require.NoError(t, err)
	require.JSONEq(t, `{"effort":"low"}`, gjson.GetBytes(result, "output_config").Raw)
}

func TestHaiku55CodexCatalogDescriptor(t *testing.T) {
	descriptor := newConfiguredCodexModelDescriptor("claude-haiku-5-5")
	require.EqualValues(t, 1_000_000, descriptor.ContextWindow)
	require.Equal(t, "Claude Haiku 5.5", descriptor.DisplayName)
	require.NotNil(t, descriptor.DefaultReasoningLevel)
	require.Equal(t, "medium", *descriptor.DefaultReasoningLevel)
	efforts := make([]string, 0, len(descriptor.SupportedReasoningLevels))
	for _, level := range descriptor.SupportedReasoningLevels {
		efforts = append(efforts, level.Effort)
	}
	require.Equal(t, []string{"low", "medium", "high", "xhigh", "max"}, efforts)
}

func TestHaiku55PricingLongContextTier(t *testing.T) {
	data, err := os.ReadFile("../../resources/model-pricing/model_prices_and_context_window.json")
	require.NoError(t, err)
	catalog := &PricingService{}
	catalog.pricingData, err = catalog.parsePricingData(data)
	require.NoError(t, err)
	sources := map[string]*BillingService{
		"billing fallback": newTestBillingService(),
		"pricing fallback": NewBillingService(&config.Config{}, &PricingService{pricingData: map[string]*LiteLLMModelPricing{
			"claude-haiku-4-5": {InputCostPerToken: 1e-6, OutputCostPerToken: 5e-6},
		}}),
		"catalog": NewBillingService(&config.Config{}, catalog),
	}
	for source, svc := range sources {
		for _, model := range []string{"claude-haiku-5-5", "anthropic/claude-haiku-5.5", "global.anthropic.claude-haiku-5-5"} {
			t.Run(source+"/"+model, func(t *testing.T) {
				for _, total := range []int{99_999, 100_000, 100_001} {
					tokens := UsageTokens{
						InputTokens: total - 2000, OutputTokens: 500,
						CacheReadTokens: 1000, CacheCreationTokens: 1000,
						CacheCreation5mTokens: 400, CacheCreation1hTokens: 600,
					}
					cost, err := svc.CalculateCost(model, tokens, 1)
					require.NoError(t, err)
					m := 1.0
					if total > 100_000 {
						m = 5
					}
					require.InDelta(t, float64(tokens.InputTokens)*0.1e-6*m, cost.InputCost, 1e-12)
					require.InDelta(t, (400*0.125e-6+600*0.2e-6)*m, cost.CacheCreationCost, 1e-12)
					require.InDelta(t, 1000*0.01e-6*m, cost.CacheReadCost, 1e-12)
					require.InDelta(t, 500*0.5e-6*m, cost.OutputCost, 1e-12)
					require.Equal(t, total > 100_000, cost.LongContextBillingApplied)
				}
			})
		}
	}
}
