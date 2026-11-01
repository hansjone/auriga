package anthropic

import "github.com/maximhq/bifrost/core/schemas"

func (resp *AnthropicCountTokensResponse) ToBifrostCountTokensResponse(model string) *schemas.BifrostCountTokensResponse {
	if resp == nil {
		return nil
	}

	totalTokens := resp.InputTokens

	bifrostResp := &schemas.BifrostCountTokensResponse{
		Model:       model,
		InputTokens: resp.InputTokens,
		TotalTokens: &totalTokens,
		Object:      "response.input_tokens",
	}

	return bifrostResp
}
