package utils

import (
	"strings"

	"github.com/maximhq/bifrost/core/schemas"
)

var knownAnthropicMaxOutputTokens = map[string]int{
	"claude-opus-5":     128000,
	"claude-mythos":     128000,
	"claude-fable-5":    128000,
	"claude-opus-4-8":   128000,
	"claude-opus-4-7":   128000,
	"claude-opus-4-6":   128000,
	"claude-sonnet-5":   128000,
	"claude-sonnet-4-6": 128000,
	"claude-haiku-4-5":  64000,
	"claude-sonnet-4-5": 64000,
	"claude-opus-4-5":   64000,
	"claude-opus-4-1":   32000,
	"claude-sonnet-4":   64000,
	"claude-opus-4":     32000,
	"claude-sonnet-4-0": 64000,
	"claude-opus-4-0":   32000,
	"claude-3-5-sonnet": 8192,
	"claude-3-5-haiku":  8192,
	"claude-3-7-sonnet": 8192,
	"claude-3-opus":     4096,
	"claude-3-sonnet":   4096,
	"claude-3-haiku":    4096,
}
var geminiBudgetRanges = []struct {
	prefix   string
	min, max int
}{
	{"gemini-2.5-flash-lite", 512, 24576},
	{"gemini-2.5-pro", 128, 32768},
	{"gemini-2.5-flash", 0, 24576},
}

func CapabilitiesFor(provider schemas.ModelProvider, model string) *schemas.ModelCapabilities {
	return schemas.CapabilitiesFor(provider, model)
}

func GetMaxOutputTokensOrDefault(provider schemas.ModelProvider, model string, defaultValue int) int {
	if caps := CapabilitiesFor(provider, model); caps != nil && caps.MaxOutputTokens != nil {
		return *caps.MaxOutputTokens
	}
	if m := KnownClaudeMaxOutputTokens(model); m > 0 {
		return m
	}
	return defaultValue
}

func KnownClaudeMaxOutputTokens(model string) int {
	if !strings.Contains(model, "claude") {
		return 0
	}
	return knownAnthropicMaxOutputTokens[normalizeClaudeModelName(model)]
}

func KnownGeminiReasoningBudget(model string) *schemas.BudgetControl {
	modelLower := strings.ToLower(model)
	for _, entry := range geminiBudgetRanges {
		if strings.Contains(modelLower, entry.prefix) {
			return &schemas.BudgetControl{Min: new(entry.min), Max: new(entry.max)}
		}
	}
	return nil
}

func normalizeClaudeModelName(model string) string {

	if idx := strings.LastIndex(model, "."); idx >= 0 {
		model = model[idx+1:]
	}

	if idx := strings.Index(model, "@"); idx >= 0 {
		model = model[:idx]
	}

	if idx := strings.Index(model, ":"); idx >= 0 {
		model = model[:idx]
		if len(model) >= 3 {
			suffix := model[len(model)-3:]
			if suffix == "-v1" || suffix == "-v2" {
				model = model[:len(model)-3]
			}
		}
	}

	if strings.HasSuffix(model, "-v1") || strings.HasSuffix(model, "-v2") {
		model = model[:len(model)-3]
	}

	return schemas.BaseModelName(model)
}
