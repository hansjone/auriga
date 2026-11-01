// Auriga modifications: trimmed relay core/providers; see third_party/bifrost/UPSTREAM.md.

package openai

import (
	"maps"

	"github.com/maximhq/bifrost/core/schemas"
)

func ToOpenAITextCompletionRequest(bifrostReq *schemas.BifrostTextCompletionRequest) *OpenAITextCompletionRequest {
	if bifrostReq == nil {
		return nil
	}
	params := bifrostReq.Params
	openaiReq := &OpenAITextCompletionRequest{
		Model:  bifrostReq.Model,
		Prompt: bifrostReq.Input,
	}
	if params != nil {
		openaiReq.TextCompletionParameters = *params

		openaiReq.TextCompletionParameters.User = SanitizeUserField(openaiReq.TextCompletionParameters.User)
		if bifrostReq.Params.ExtraParams != nil {
			openaiReq.ExtraParams = maps.Clone(bifrostReq.Params.ExtraParams)
			openaiReq.TextCompletionParameters.ExtraParams = openaiReq.ExtraParams
		}
	}
	if bifrostReq.Provider == schemas.Fireworks {
		openaiReq.applyFireworksTextCompletionCompatibility()
	}
	return openaiReq
}

func (req *OpenAITextCompletionRequest) applyFireworksTextCompletionCompatibility() {
	if req == nil || req.ExtraParams == nil {
		return
	}

	if req.PromptCacheIsolationKey == nil {
		if value, ok := req.ExtraParams["prompt_cache_key"]; ok {
			switch typed := value.(type) {
			case string:
				if typed != "" {
					req.PromptCacheIsolationKey = &typed
				}
			case *string:
				if typed != nil && *typed != "" {
					req.PromptCacheIsolationKey = typed
				}
			}
		}
	}
	delete(req.ExtraParams, "prompt_cache_key")
	req.TextCompletionParameters.ExtraParams = req.ExtraParams
}
