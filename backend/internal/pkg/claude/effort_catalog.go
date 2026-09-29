package claude

import (
	"strings"
	"unicode"
)

var (
	effortLowMediumHigh         = []string{"low", "medium", "high"}
	effortLowMediumHighMax      = []string{"low", "medium", "high", "max"}
	effortLowMediumHighXHighMax = []string{"low", "medium", "high", "xhigh", "max"}
)

var effortFamilies = []struct {
	family string
	levels []string
}{
	{family: "claude-mythos-preview", levels: effortLowMediumHighMax},
	{family: "claude-mythos-5", levels: effortLowMediumHighXHighMax},
	{family: "claude-fable-5", levels: effortLowMediumHighXHighMax},
	{family: "claude-sonnet-4-6", levels: effortLowMediumHighMax},
	{family: "claude-sonnet-5-5", levels: effortLowMediumHighXHighMax},
	{family: "claude-sonnet-5", levels: effortLowMediumHighXHighMax},
	{family: "claude-opus-4-8", levels: effortLowMediumHighXHighMax},
	{family: "claude-opus-4-7", levels: effortLowMediumHighXHighMax},
	{family: "claude-opus-4-6", levels: effortLowMediumHighMax},
	{family: "claude-opus-4-5", levels: effortLowMediumHigh},
	{family: "claude-opus-5-5", levels: effortLowMediumHighXHighMax},
	{family: "claude-opus-5", levels: effortLowMediumHighXHighMax},
}

// EffortLevelsForModel returns the output_config.effort values accepted by a
// Claude model, ordered from the lightest to the deepest reasoning level.
func EffortLevelsForModel(model string) []string {
	id := normalizeEffortModelID(model)
	for _, entry := range effortFamilies {
		if id == entry.family || strings.HasPrefix(id, entry.family+"-") {
			return append([]string(nil), entry.levels...)
		}
	}
	return nil
}

// IsOpus55 identifies the fixed Opus 5.5 ID after provider/local suffix normalization.
func IsOpus55(model string) bool {
	return normalizeEffortModelID(model) == "claude-opus-5-5"
}

// IsSonnet55 identifies the fixed Sonnet 5.5 ID after provider/local suffix normalization.
func IsSonnet55(model string) bool {
	return normalizeEffortModelID(model) == "claude-sonnet-5-5"
}

// IsClaude55 identifies the Claude 5.5 generation (Opus 5.5 and Sonnet 5.5).
// These models share the same request contract: thinking is on without a
// thinking field, disabled/manual thinking and forced tool_choice return 400,
// and thinking blocks are signed over the conversation and must be replayed
// unchanged.
func IsClaude55(model string) bool {
	return Claude55ModelID(model) != ""
}

// Claude55ModelID returns the canonical Claude API ID of a Claude 5.5 model,
// or an empty string when model is not part of the 5.5 generation.
func Claude55ModelID(model string) string {
	switch id := normalizeEffortModelID(model); id {
	case "claude-opus-5-5", "claude-sonnet-5-5":
		return id
	}
	return ""
}

func normalizeEffortModelID(model string) string {
	id := strings.ToLower(strings.TrimSpace(model))
	id = strings.TrimPrefix(id, "models/")
	if slash := strings.IndexByte(id, '/'); slash >= 0 {
		id = strings.TrimPrefix(strings.TrimSpace(id[slash+1:]), "models/")
	}
	id = strings.TrimPrefix(id, "anthropic.")
	id = strings.TrimSuffix(id, "-thinking")
	// OpenRouter uses a dotted minor version for the exact Claude 5.5 IDs.
	// Normalize them before effort, thinking, and billing family lookups.
	switch id {
	case "claude-opus-5.5":
		id = "claude-opus-5-5"
	case "claude-sonnet-5.5":
		id = "claude-sonnet-5-5"
	}
	if mapped, ok := ModelIDReverseOverrides[id]; ok {
		id = mapped
	}
	if len(id) >= 9 {
		suffix := id[len(id)-9:]
		if suffix[0] == '-' {
			digits := true
			for _, r := range suffix[1:] {
				if !unicode.IsDigit(r) {
					digits = false
					break
				}
			}
			if digits {
				id = id[:len(id)-9]
			}
		}
	}
	return id
}
