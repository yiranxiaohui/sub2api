package claude

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEffortLevelsForModel(t *testing.T) {
	t.Parallel()

	tests := []struct {
		model string
		want  []string
	}{
		{model: "claude-opus-4-6", want: []string{"low", "medium", "high", "max"}},
		{model: "anthropic/claude-sonnet-4-6", want: []string{"low", "medium", "high", "max"}},
		{model: "claude-opus-5", want: []string{"low", "medium", "high", "xhigh", "max"}},
		{model: "anthropic/claude-opus-5.5", want: []string{"low", "medium", "high", "xhigh", "max"}},
		{model: "claude-sonnet-5-5", want: []string{"low", "medium", "high", "xhigh", "max"}},
		{model: "anthropic/claude-sonnet-5.5", want: []string{"low", "medium", "high", "xhigh", "max"}},
		{model: "claude-opus-4-5-20251101", want: []string{"low", "medium", "high"}},
		{model: "claude-haiku-4-5-20251001", want: nil},
		{model: "gpt-5.6", want: nil},
	}
	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, EffortLevelsForModel(tt.model))
		})
	}
}

func TestIsOpus55OpenRouterExactAlias(t *testing.T) {
	t.Parallel()
	for _, model := range []string{"claude-opus-5-5", "anthropic/claude-opus-5.5"} {
		require.True(t, IsOpus55(model), model)
	}
	for _, model := range []string{"claude-opus-5", "anthropic/claude-opus-5.6", "anthropic/claude-opus-5.5-preview"} {
		require.False(t, IsOpus55(model), model)
	}
}

func TestIsSonnet55AndClaude55Family(t *testing.T) {
	t.Parallel()
	for _, model := range []string{"claude-sonnet-5-5", "anthropic/claude-sonnet-5.5", "anthropic.claude-sonnet-5-5", "claude-sonnet-5-5-thinking"} {
		require.True(t, IsSonnet55(model), model)
		require.True(t, IsClaude55(model), model)
		require.False(t, IsOpus55(model), model)
		require.Equal(t, "claude-sonnet-5-5", Claude55ModelID(model), model)
	}
	require.True(t, IsClaude55("claude-opus-5-5"))
	require.Equal(t, "claude-opus-5-5", Claude55ModelID("anthropic/claude-opus-5.5"))
	for _, model := range []string{"claude-sonnet-5", "claude-sonnet-4-6", "anthropic/claude-sonnet-5.6", "claude-opus-5", "gpt-5.5"} {
		require.False(t, IsSonnet55(model), model)
		require.False(t, IsClaude55(model), model)
		require.Empty(t, Claude55ModelID(model), model)
	}
}
