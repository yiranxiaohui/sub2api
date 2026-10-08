package apicompat

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestHaiku55ResponsesThinkingAndSampling(t *testing.T) {
	for _, effort := range []string{"", "low", "medium", "high", "xhigh", "max", "none"} {
		req := &ResponsesRequest{Model: "claude-haiku-5-5", Input: json.RawMessage(`"hello"`), Reasoning: &ResponsesReasoning{Effort: effort}}
		out, err := ResponsesToAnthropicRequest(req)
		require.NoError(t, err, effort)
		require.Zero(t, out.Thinking.BudgetTokens)
		if effort == "none" {
			require.Equal(t, "disabled", out.Thinking.Type)
			require.Nil(t, out.OutputConfig)
			continue
		}
		require.Equal(t, "adaptive", out.Thinking.Type)
		if effort == "" {
			effort = "medium"
		}
		require.Equal(t, effort, out.OutputConfig.Effort)
	}

	// Haiku 5.5 accepts forced tool_choice, unlike Opus/Sonnet 5.5.
	for _, choice := range []string{`"required"`, `{"type":"function","name":"lookup"}`} {
		out, err := ResponsesToAnthropicRequest(&ResponsesRequest{Model: "claude-haiku-5-5", Input: json.RawMessage(`"hello"`), ToolChoice: json.RawMessage(choice)})
		require.NoError(t, err, choice)
		require.NotEmpty(t, out.ToolChoice)
	}

	_, err := ResponsesToAnthropicRequest(&ResponsesRequest{Model: "claude-haiku-5-5", Input: json.RawMessage(`"hello"`), Reasoning: &ResponsesReasoning{Effort: "minimal"}})
	require.ErrorContains(t, err, "reasoning effort")

	temperature, topP := 0.7, 1.0
	_, err = ResponsesToAnthropicRequest(&ResponsesRequest{Model: "claude-haiku-5-5", Input: json.RawMessage(`"hello"`), Temperature: &temperature})
	require.ErrorContains(t, err, "temperature")
	_, err = ResponsesToAnthropicRequest(&ResponsesRequest{Model: "claude-haiku-5-5", Input: json.RawMessage(`"hello"`), TopP: &topP})
	require.ErrorContains(t, err, "top_p")

	temperature, topP = 1, 0.99
	_, err = ResponsesToAnthropicRequest(&ResponsesRequest{Model: "claude-haiku-5-5", Input: json.RawMessage(`"hello"`), Temperature: &temperature})
	require.NoError(t, err)
	_, err = ResponsesToAnthropicRequest(&ResponsesRequest{Model: "claude-haiku-5-5", Input: json.RawMessage(`"hello"`), TopP: &topP})
	require.NoError(t, err)
	_, err = ResponsesToAnthropicRequest(&ResponsesRequest{Model: "claude-haiku-5-5", Input: json.RawMessage(`"hello"`), Temperature: &temperature, TopP: &topP})
	require.ErrorContains(t, err, "together")
}

func TestHaiku55SignedThinkingResponsesRoundTrip(t *testing.T) {
	block := AnthropicContentBlock{Type: "thinking", Thinking: "", Signature: "signed-haiku-block"}
	response := AnthropicToResponsesResponse(&AnthropicResponse{Model: "claude-haiku-5-5", Content: []AnthropicContentBlock{block, {Type: "text", Text: "progress"}, {Type: "tool_use", ID: "toolu_1", Name: "lookup", Input: json.RawMessage(`{}`)}}})
	require.Len(t, response.Output, 3)
	require.NotEmpty(t, response.Output[0].EncryptedContent)
	raw, err := json.Marshal(response.Output)
	require.NoError(t, err)
	var items []ResponsesInputItem
	require.NoError(t, json.Unmarshal(raw, &items))
	items = append(items, ResponsesInputItem{Type: "function_call_output", CallID: response.Output[2].CallID, Output: "ok"})
	raw, err = json.Marshal(items)
	require.NoError(t, err)
	converted, err := ResponsesToAnthropicRequest(&ResponsesRequest{Model: "claude-haiku-5-5", Input: raw})
	require.NoError(t, err)
	var blocks []AnthropicContentBlock
	require.NoError(t, json.Unmarshal(converted.Messages[0].Content, &blocks))
	require.Equal(t, block, blocks[0])
}
