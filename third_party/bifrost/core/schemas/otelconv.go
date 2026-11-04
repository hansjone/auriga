// Auriga modifications: trimmed relay core/providers; see third_party/bifrost/UPSTREAM.md.

package schemas

func OTelOperationName(rt RequestType) string {
	switch rt {
	case ChatCompletionRequest, ChatCompletionStreamRequest,
		ResponsesRequest, ResponsesStreamRequest:
		return "chat"
	case TextCompletionRequest, TextCompletionStreamRequest:
		return "text_completion"
	case EmbeddingRequest:
		return "embeddings"
	case SpeechRequest, SpeechStreamRequest,
		TranscriptionRequest, TranscriptionStreamRequest,
		ImageGenerationRequest, ImageGenerationStreamRequest,
		ImageEditRequest, ImageEditStreamRequest:
		return "generate_content"
	default:
		return string(rt)
	}
}

func OTelProviderName(p ModelProvider) string {
	switch p {
	case Bedrock:
		return "aws.bedrock"
	case Vertex:
		return "gcp.vertex_ai"
	case Gemini:
		return "gcp.gemini"
	case XAI:
		return "x_ai"
	case Mistral:
		return "mistral_ai"
	case Azure:
		return "azure.ai.openai"
	default:
		return string(p)
	}
}
