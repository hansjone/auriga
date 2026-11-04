// Auriga modifications: trimmed relay core/providers; see third_party/bifrost/UPSTREAM.md.

package anthropic

import (
	"fmt"
	"strings"

	providerUtils "github.com/maximhq/bifrost/core/providers/utils"
	"github.com/maximhq/bifrost/core/schemas"
)

func ToAnthropicTextCompletionRequest(bifrostReq *schemas.BifrostTextCompletionRequest) *AnthropicTextRequest {
	if bifrostReq == nil {
		return nil
	}

	prompt := ""
	if bifrostReq.Input.PromptStr != nil {
		prompt = *bifrostReq.Input.PromptStr
	} else if len(bifrostReq.Input.PromptArray) > 0 {
		prompt = strings.Join(bifrostReq.Input.PromptArray, "\n\n")
	}

	anthropicReq := &AnthropicTextRequest{
		Model:             bifrostReq.Model,
		Prompt:            frameAnthropicTextPrompt(prompt),
		MaxTokensToSample: providerUtils.GetMaxOutputTokensOrDefault(bifrostReq.Provider, bifrostReq.Model, AnthropicDefaultMaxTokens),
	}

	if bifrostReq.Params != nil {
		if bifrostReq.Params.MaxTokens != nil {
			anthropicReq.MaxTokensToSample = *bifrostReq.Params.MaxTokens
		}
		anthropicReq.Temperature = bifrostReq.Params.Temperature
		anthropicReq.TopP = bifrostReq.Params.TopP
		anthropicReq.StopSequences = bifrostReq.Params.Stop

		if bifrostReq.Params.ExtraParams != nil {
			anthropicReq.ExtraParams = bifrostReq.Params.ExtraParams
			if topK, ok := schemas.SafeExtractIntPointer(bifrostReq.Params.ExtraParams["top_k"]); ok {
				delete(anthropicReq.ExtraParams, "top_k")
				anthropicReq.TopK = topK
			}
		}
	}

	return anthropicReq
}

const (
	anthropicHumanTurn     = "\n\nHuman:"
	anthropicAssistantTurn = "\n\nAssistant:"
)

func frameAnthropicTextPrompt(prompt string) string {
	if !strings.HasPrefix(prompt, anthropicHumanTurn) {
		return fmt.Sprintf("%s %s%s", anthropicHumanTurn, prompt, anthropicAssistantTurn)
	}
	if strings.HasSuffix(strings.TrimRight(prompt, " "), anthropicAssistantTurn) {
		return prompt
	}
	return prompt + anthropicAssistantTurn
}

func (response *AnthropicTextResponse) ToBifrostTextCompletionResponse() *schemas.BifrostTextCompletionResponse {
	if response == nil {
		return nil
	}
	return &schemas.BifrostTextCompletionResponse{
		ID:     response.ID,
		Object: "text_completion",
		Choices: []schemas.BifrostResponseChoice{
			{
				Index: 0,
				TextCompletionResponseChoice: &schemas.TextCompletionResponseChoice{
					Text: &response.Completion,
				},
			},
		},
		Usage: &schemas.BifrostLLMUsage{
			PromptTokens:     response.Usage.InputTokens,
			CompletionTokens: response.Usage.OutputTokens,
			TotalTokens:      response.Usage.InputTokens + response.Usage.OutputTokens,
		},
		Model: response.Model,
	}
}
