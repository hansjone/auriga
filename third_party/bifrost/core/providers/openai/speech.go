package openai

import "github.com/maximhq/bifrost/core/schemas"

func ToOpenAISpeechRequest(bifrostReq *schemas.BifrostSpeechRequest) *OpenAISpeechRequest {
	if bifrostReq == nil || bifrostReq.Input.Input == "" {
		return nil
	}

	speechInput := bifrostReq.Input
	params := bifrostReq.Params

	openaiReq := &OpenAISpeechRequest{
		Model: bifrostReq.Model,
		Input: speechInput.Input,
	}

	if params != nil {
		openaiReq.SpeechParameters = *params
	}

	if bifrostReq.Params != nil {
		openaiReq.ExtraParams = bifrostReq.Params.ExtraParams
	}
	return openaiReq
}
