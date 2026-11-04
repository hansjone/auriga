// Auriga modifications: trimmed relay core/providers; see third_party/bifrost/UPSTREAM.md.

package gemini

import (
	"context"
	"fmt"
	"strings"

	"github.com/maximhq/bifrost/core/providers/utils"
	"github.com/maximhq/bifrost/core/schemas"
)

func ToGeminiSpeechRequest(bifrostReq *schemas.BifrostSpeechRequest) (*GeminiGenerationRequest, error) {
	if bifrostReq == nil {
		return nil, fmt.Errorf("bifrostReq is nil")
	}

	if bifrostReq.Params != nil && bifrostReq.Params.ResponseFormat != "" && bifrostReq.Params.ResponseFormat != "wav" {
		return nil, fmt.Errorf("gemini does not support response_format: %s. Only wav or empty string is supported which defaults to wav", bifrostReq.Params.ResponseFormat)
	}

	geminiReq := &GeminiGenerationRequest{
		Model: bifrostReq.Model,
	}

	geminiReq.GenerationConfig.ResponseModalities = []Modality{ModalityAudio}

	if bifrostReq.Input != nil && bifrostReq.Input.Input != "" {
		geminiReq.Contents = []Content{
			{
				Parts: []*Part{
					{
						Text: bifrostReq.Input.Input,
					},
				},
			},
		}

		if bifrostReq.Params != nil && bifrostReq.Params.VoiceConfig != nil {

			if bifrostReq.Params.VoiceConfig.Voice != nil || len(bifrostReq.Params.VoiceConfig.MultiVoiceConfig) > 0 {
				addSpeechConfigToGenerationConfig(&geminiReq.GenerationConfig, bifrostReq.Params.VoiceConfig)
			}
			geminiReq.ExtraParams = bifrostReq.Params.ExtraParams
		}
	}
	return geminiReq, nil
}

func (response *GenerateContentResponse) ToBifrostSpeechResponse(ctx context.Context) (*schemas.BifrostSpeechResponse, error) {
	bifrostResp := &schemas.BifrostSpeechResponse{}

	if len(response.Candidates) > 0 {
		candidate := response.Candidates[0]
		if candidate.Content != nil && len(candidate.Content.Parts) > 0 {
			var audioData []byte

			for _, part := range candidate.Content.Parts {
				if part.InlineData != nil && len(part.InlineData.Data) > 0 {

					if strings.HasPrefix(part.InlineData.MIMEType, "audio/") {
						decodedData, err := decodeBase64StringToBytes(part.InlineData.Data)
						if err != nil {
							return nil, fmt.Errorf("failed to decode base64 audio data: %v", err)
						}
						audioData = append(audioData, decodedData...)
					}
				}
			}
			if len(audioData) > 0 {
				responseFormat := ctx.Value(BifrostContextKeyResponseFormat).(string)

				if responseFormat == "wav" {
					wavData, err := utils.ConvertPCMToWAV(audioData, utils.DefaultGeminiPCMConfig())
					if err != nil {
						return nil, fmt.Errorf("failed to convert PCM to WAV: %v", err)
					}
					bifrostResp.Audio = wavData
				} else {
					bifrostResp.Audio = audioData
				}
			}

			if response.UsageMetadata != nil {
				bifrostResp.Usage = convertGeminiUsageMetadataToSpeechUsage(response.UsageMetadata)
			}
		}
	}
	return bifrostResp, nil
}
