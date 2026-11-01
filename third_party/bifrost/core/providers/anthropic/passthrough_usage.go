// Auriga modifications: trimmed relay core/providers; see third_party/bifrost/UPSTREAM.md.

package anthropic

import (
	"strings"

	"github.com/bytedance/sonic"
	providerUtils "github.com/maximhq/bifrost/core/providers/utils"
	"github.com/maximhq/bifrost/core/schemas"
)

type AnthropicPassthroughStreamUsage struct {
	combined   AnthropicUsage
	iterations []AnthropicUsage
	seen       bool
}

func ClassifyPassthroughPath(path string) schemas.RequestType {
	if idx := strings.IndexByte(path, '?'); idx >= 0 {
		path = path[:idx]
	}

	switch {
	case strings.HasSuffix(path, "/messages"):
		return schemas.ResponsesRequest
	case strings.HasSuffix(path, "/messages/count_tokens"):
		return schemas.CountTokensRequest
	case strings.HasSuffix(path, "/complete"):
		return schemas.TextCompletionRequest
	}
	return ""
}

func ExtractAnthropicPassthroughUsage(path string, _, body []byte) *schemas.BifrostPassthroughUsage {
	switch ClassifyPassthroughPath(path) {
	case schemas.ResponsesRequest:
		return extractAnthropicMessagesUsage(body)
	case schemas.TextCompletionRequest:
		return extractAnthropicCompleteUsage(body)
	}
	return nil
}

func HasAnthropicPassthroughUsage(event []byte) bool {
	return providerUtils.GetJSONField(event, "usage").Exists() ||
		providerUtils.GetJSONField(event, "message.usage").Exists()
}

func convertAnthropicUsageIterations(iterations []AnthropicUsage) []schemas.ResponsesResponseUsage {
	if len(iterations) == 0 {
		return nil
	}
	out := make([]schemas.ResponsesResponseUsage, 0, len(iterations))
	for i := range iterations {
		if converted := ConvertAnthropicUsageToBifrostUsage(&iterations[i]); converted != nil {
			out = append(out, *converted)
		}
	}
	return out
}

func buildAnthropicPassthroughUsage(au *AnthropicUsage) *schemas.BifrostPassthroughUsage {
	if au == nil {
		return nil
	}
	iterations := au.Iterations
	au = billableAnthropicUsage(au)
	totalInput := au.InputTokens + au.CacheReadInputTokens + au.CacheCreationInputTokens
	total := totalInput + au.OutputTokens
	if total == 0 {
		return nil
	}

	usage := &schemas.BifrostLLMUsage{
		PromptTokens:     totalInput,
		CompletionTokens: au.OutputTokens,
		TotalTokens:      total,
	}

	if au.CacheReadInputTokens > 0 || au.CacheCreationInputTokens > 0 {
		details := &schemas.ChatPromptTokensDetails{
			CachedReadTokens:  au.CacheReadInputTokens,
			CachedWriteTokens: au.CacheCreationInputTokens,
		}
		if au.CacheCreation.Ephemeral5mInputTokens > 0 || au.CacheCreation.Ephemeral1hInputTokens > 0 {
			details.CachedWriteTokenDetails = &schemas.ChatCachedWriteTokenDetails{
				CachedWriteTokens5m: au.CacheCreation.Ephemeral5mInputTokens,
				CachedWriteTokens1h: au.CacheCreation.Ephemeral1hInputTokens,
			}
		}
		usage.PromptTokensDetails = details
	}

	if au.ServerToolUse != nil && au.ServerToolUse.WebSearchRequests > 0 {
		n := au.ServerToolUse.WebSearchRequests
		usage.CompletionTokensDetails = &schemas.ChatCompletionTokensDetails{
			NumSearchQueries: &n,
		}
		usage.ToolUsage = &schemas.ToolUsage{WebSearch: &schemas.WebSearchToolUsage{NumRequests: n}}
	}

	if au.OutputTokensDetails != nil && au.OutputTokensDetails.ThinkingTokens > 0 {
		if usage.CompletionTokensDetails == nil {
			usage.CompletionTokensDetails = &schemas.ChatCompletionTokensDetails{}
		}
		usage.CompletionTokensDetails.ReasoningTokens = au.OutputTokensDetails.ThinkingTokens
	}

	u := &schemas.BifrostPassthroughUsage{LLMUsage: usage, Iterations: convertAnthropicUsageIterations(iterations)}
	if au.ServiceTier != nil {
		t := MapAnthropicServiceTierToBifrost(*au.ServiceTier)
		u.ServiceTier = &t
	}
	if au.Speed != nil {
		u.Speed = au.Speed
	}
	if au.InferenceGeo != nil {
		u.InferenceGeo = au.InferenceGeo
	}
	return u
}

func (a *AnthropicPassthroughStreamUsage) ObserveEvent(event []byte) *schemas.BifrostPassthroughUsage {
	var evt AnthropicStreamEvent
	if err := sonic.Unmarshal(event, &evt); err != nil {
		return a.usage()
	}

	var u *AnthropicUsage
	if evt.Usage != nil {
		u = evt.Usage
	} else if evt.Message != nil && evt.Message.Usage != nil {
		u = evt.Message.Usage
	}
	if u == nil {
		return a.usage()
	}

	if len(u.Iterations) > 0 {
		a.iterations = u.Iterations
	}
	u = billableAnthropicUsage(u)

	a.seen = true
	c := &a.combined
	if u.InputTokens > c.InputTokens {
		c.InputTokens = u.InputTokens
	}
	if u.OutputTokens > c.OutputTokens {
		c.OutputTokens = u.OutputTokens
	}
	if u.CacheReadInputTokens > c.CacheReadInputTokens {
		c.CacheReadInputTokens = u.CacheReadInputTokens
	}
	if u.CacheCreationInputTokens > c.CacheCreationInputTokens {
		c.CacheCreationInputTokens = u.CacheCreationInputTokens
	}
	if u.CacheCreation.Ephemeral5mInputTokens > c.CacheCreation.Ephemeral5mInputTokens {
		c.CacheCreation.Ephemeral5mInputTokens = u.CacheCreation.Ephemeral5mInputTokens
	}
	if u.CacheCreation.Ephemeral1hInputTokens > c.CacheCreation.Ephemeral1hInputTokens {
		c.CacheCreation.Ephemeral1hInputTokens = u.CacheCreation.Ephemeral1hInputTokens
	}
	if u.ServerToolUse != nil {
		if c.ServerToolUse == nil {
			c.ServerToolUse = &AnthropicServerToolUseUsage{}
		}
		if u.ServerToolUse.WebSearchRequests > c.ServerToolUse.WebSearchRequests {
			c.ServerToolUse.WebSearchRequests = u.ServerToolUse.WebSearchRequests
		}
	}
	if u.OutputTokensDetails != nil {
		if c.OutputTokensDetails == nil {
			c.OutputTokensDetails = &AnthropicOutputTokensDetails{}
		}
		if u.OutputTokensDetails.ThinkingTokens > c.OutputTokensDetails.ThinkingTokens {
			c.OutputTokensDetails.ThinkingTokens = u.OutputTokensDetails.ThinkingTokens
		}
	}
	if u.ServiceTier != nil {
		c.ServiceTier = u.ServiceTier
	}
	if u.Speed != nil {
		c.Speed = u.Speed
	}
	if u.InferenceGeo != nil {
		c.InferenceGeo = u.InferenceGeo
	}
	return a.usage()
}

func (a *AnthropicPassthroughStreamUsage) usage() *schemas.BifrostPassthroughUsage {
	if !a.seen {
		return nil
	}
	u := buildAnthropicPassthroughUsage(&a.combined)
	if u != nil {
		u.Iterations = convertAnthropicUsageIterations(a.iterations)
	}
	return u
}

func extractAnthropicMessagesUsage(body []byte) *schemas.BifrostPassthroughUsage {
	if len(body) == 0 {
		return nil
	}
	var resp AnthropicMessageResponse
	if err := sonic.Unmarshal(body, &resp); err != nil || resp.Usage == nil {
		return nil
	}
	return buildAnthropicPassthroughUsage(resp.Usage)
}

func extractAnthropicCompleteUsage(body []byte) *schemas.BifrostPassthroughUsage {
	if len(body) == 0 {
		return nil
	}
	var resp struct {
		Usage *struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := sonic.Unmarshal(body, &resp); err != nil || resp.Usage == nil {
		return nil
	}
	total := resp.Usage.InputTokens + resp.Usage.OutputTokens
	if total == 0 {
		return nil
	}
	return &schemas.BifrostPassthroughUsage{
		LLMUsage: &schemas.BifrostLLMUsage{
			PromptTokens:     resp.Usage.InputTokens,
			CompletionTokens: resp.Usage.OutputTokens,
			TotalTokens:      total,
		},
	}
}
