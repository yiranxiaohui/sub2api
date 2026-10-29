package service

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// 本文件锁住两类 thinking 块预过滤行为：
//   - Fable 5 / 5.1 / Opus 5 默认开启 thinking，客户端省略 thinking 字段时，历史中的
//     签名 thinking 块必须原样送达上游，由上游负责校验签名；
//   - 管理员开启「Thinking 块原样透传」后，预过滤整体跳过。

func resetThinkingBlockPassthroughCacheForTest(t *testing.T) {
	t.Helper()
	reset := func() {
		thinkingBlockPassthroughSF.Forget("thinking_block_passthrough")
		thinkingBlockPassthroughCache.Store(&cachedThinkingBlockPassthrough{})
	}
	reset()
	t.Cleanup(reset)
}

func TestDefaultThinkingModelsKeepSignedHistoryWithoutThinkingField(t *testing.T) {
	// 签名是否被篡改只有上游能判定；网关只剥离缺签名的块。
	body := []byte(`{"model":"m","messages":[{"role":"user","content":"hi"},{"role":"assistant","content":[{"type":"thinking","thinking":"tampered text","signature":"tampered-sig"},{"type":"redacted_thinking","data":"encrypted"},{"type":"text","text":"answer"}]},{"role":"user","content":"next"}]}`)
	for _, model := range []string{"claude-fable-5", "claude-fable-5-1", "anthropic/claude-fable-5.1", "claude-opus-5"} {
		t.Run(model, func(t *testing.T) {
			require.Equal(t, string(body), string(FilterThinkingBlocks(body, model)))
		})
	}
}

func TestDefaultThinkingModelsDropOnlyUnsignedBlocks(t *testing.T) {
	body := []byte(`{"model":"claude-fable-5-1","messages":[{"role":"assistant","content":[{"type":"thinking","thinking":"old","signature":""},{"type":"text","text":"first"}]},{"role":"assistant","content":[{"type":"thinking","thinking":"later","signature":"signed"},{"type":"text","text":"second"}]}]}`)
	for _, model := range []string{"claude-fable-5-1", "claude-opus-5"} {
		t.Run(model, func(t *testing.T) {
			out := FilterThinkingBlocks(body, model)
			require.False(t, gjson.GetBytes(out, "messages.0.content.#(type==thinking)").Exists())
			require.Equal(t, "first", gjson.GetBytes(out, "messages.0.content.0.text").String())
			// 与 5.5 不同：一个缺签名块不会连带清空后续签名历史。
			require.Equal(t, "signed", gjson.GetBytes(out, "messages.1.content.0.signature").String())
		})
	}
}

func TestDefaultThinkingModelsHonorDisabledThinking(t *testing.T) {
	body := []byte(`{"thinking":{"type":"disabled"},"messages":[{"role":"assistant","content":[{"type":"thinking","thinking":"t","signature":"signed"},{"type":"text","text":"a"}]}]}`)
	// Opus 5 可显式关闭 thinking，历史 thinking 块照旧剥离。
	require.False(t, gjson.GetBytes(FilterThinkingBlocks(body, "claude-opus-5"), "messages.0.content.#(type==thinking)").Exists())
	// Fable 5 为 always-on adaptive thinking，签名历史仍需保留。
	require.Equal(t, string(body), string(FilterThinkingBlocks(body, "claude-fable-5")))
}

func TestFilterThinkingBlocksKeepsRedactedThinkingWhenThinkingEnabled(t *testing.T) {
	body := []byte(`{"thinking":{"type":"adaptive"},"messages":[{"role":"assistant","content":[{"type":"thinking","thinking":"t","signature":"signed"},{"type":"redacted_thinking","data":"encrypted"},{"type":"text","text":"a"}]}]}`)
	require.Equal(t, string(body), string(FilterThinkingBlocks(body, "claude-sonnet-4-5")))

	emptyData := []byte(`{"thinking":{"type":"adaptive"},"messages":[{"role":"assistant","content":[{"type":"redacted_thinking","data":""},{"type":"text","text":"a"}]}]}`)
	out := FilterThinkingBlocks(emptyData, "claude-sonnet-4-5")
	require.False(t, gjson.GetBytes(out, "messages.0.content.#(type==redacted_thinking)").Exists())
	require.Equal(t, "a", gjson.GetBytes(out, "messages.0.content.0.text").String())
}

func TestParseGatewayRequestMarksDefaultThinkingModels(t *testing.T) {
	tests := []struct {
		body string
		want bool
	}{
		{body: `{"model":"claude-fable-5-1","messages":[{"role":"user","content":"hi"}]}`, want: true},
		{body: `{"model":"claude-opus-5","messages":[{"role":"user","content":"hi"}]}`, want: true},
		{body: `{"model":"claude-opus-5","thinking":{"type":"disabled"},"messages":[{"role":"user","content":"hi"}]}`, want: false},
		{body: `{"model":"claude-sonnet-4-5","messages":[{"role":"user","content":"hi"}]}`, want: false},
	}
	for _, tt := range tests {
		parsed, err := ParseGatewayRequest(NewRequestBodyRef([]byte(tt.body)), PlatformAnthropic)
		require.NoError(t, err, tt.body)
		require.Equal(t, tt.want, parsed.ThinkingEnabled, tt.body)
	}
}

func TestThinkingBlockPassthroughSetting(t *testing.T) {
	resetThinkingBlockPassthroughCacheForTest(t)
	ctx := context.Background()

	repo := &gatewayTTLSettingRepo{data: map[string]string{}}
	svc := NewSettingService(repo, &config.Config{})
	require.False(t, svc.IsThinkingBlockPassthroughEnabled(ctx), "未配置时保持预过滤")

	// 升级前保存的配置没有该字段，读出必须仍是 false。
	repo.data[SettingKeyRectifierSettings] = `{"enabled":true,"thinking_signature_enabled":true,"thinking_budget_enabled":true}`
	resetThinkingBlockPassthroughCacheForTest(t)
	require.False(t, svc.IsThinkingBlockPassthroughEnabled(ctx))

	settings := DefaultRectifierSettings()
	settings.ThinkingBlockPassthrough = true
	require.NoError(t, svc.SetRectifierSettings(ctx, settings))
	require.True(t, svc.IsThinkingBlockPassthroughEnabled(ctx), "保存后立即生效，无需等缓存过期")
	require.True(t, gjson.Get(repo.data[SettingKeyRectifierSettings], "thinking_block_passthrough").Bool())
}

func TestForwardThinkingBlockPreFilterHonorsPassthroughSetting(t *testing.T) {
	gin.SetMode(gin.TestMode)
	resetThinkingBlockPassthroughCacheForTest(t)
	gatewayForwardingCache.Store(&cachedGatewayForwardingSettings{})
	t.Cleanup(func() { gatewayForwardingCache.Store(&cachedGatewayForwardingSettings{}) })

	forward := func(t *testing.T, settingService *SettingService, body string) []byte {
		t.Helper()
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
		parsed, err := ParseGatewayRequest(NewRequestBodyRef([]byte(body)), PlatformAnthropic)
		require.NoError(t, err)

		upstream := &anthropicHTTPUpstreamRecorder{resp: &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"id":"msg_1","type":"message","role":"assistant","model":"claude-sonnet-4-5","content":[{"type":"text","text":"ok"}],"usage":{"input_tokens":3,"output_tokens":1}}`)),
		}}
		svc := newForwardPartialUsageServiceForTest(upstream)
		svc.settingService = settingService
		_, err = svc.Forward(context.Background(), c, newAnthropicOAuthAccountForPartialUsageTest(), parsed)
		require.NoError(t, err)
		require.True(t, json.Valid(upstream.lastBody))
		return upstream.lastBody
	}

	t.Run("fable 5.1 signed history reaches upstream", func(t *testing.T) {
		body := `{"model":"claude-fable-5-1","messages":[{"role":"user","content":"hi"},{"role":"assistant","content":[{"type":"thinking","thinking":"t","signature":"tampered-sig"},{"type":"text","text":"a"}]},{"role":"user","content":"next"}]}`
		sent := forward(t, nil, body)
		require.Equal(t, "tampered-sig", gjson.GetBytes(sent, "messages.1.content.#(type==thinking).signature").String())
	})

	unsigned := `{"model":"claude-sonnet-4-5","thinking":{"type":"adaptive"},"messages":[{"role":"user","content":"hi"},{"role":"assistant","content":[{"type":"thinking","thinking":"t","signature":""},{"type":"text","text":"a"}]},{"role":"user","content":"next"}]}`

	t.Run("default pre-filter strips unsigned block", func(t *testing.T) {
		repo := &gatewayTTLSettingRepo{data: map[string]string{}}
		sent := forward(t, NewSettingService(repo, &config.Config{}), unsigned)
		require.False(t, gjson.GetBytes(sent, "messages.1.content.#(type==thinking)").Exists())
	})

	t.Run("passthrough forwards unsigned block as-is", func(t *testing.T) {
		repo := &gatewayTTLSettingRepo{data: map[string]string{}}
		settingService := NewSettingService(repo, &config.Config{})
		settings := DefaultRectifierSettings()
		settings.ThinkingBlockPassthrough = true
		require.NoError(t, settingService.SetRectifierSettings(context.Background(), settings))

		sent := forward(t, settingService, unsigned)
		require.True(t, gjson.GetBytes(sent, "messages.1.content.#(type==thinking)").Exists())
		require.Equal(t, "", gjson.GetBytes(sent, "messages.1.content.#(type==thinking).signature").String())
	})
}
