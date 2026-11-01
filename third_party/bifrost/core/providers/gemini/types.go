// Auriga modifications: trimmed relay core/providers; see third_party/bifrost/UPSTREAM.md.

package gemini

import (
	"encoding/base64"
	"encoding/json"
	"fmt"

	"reflect"
	"strconv"
	"strings"
	"time"

	"cloud.google.com/go/civil"
	"github.com/bytedance/sonic"
	providerUtils "github.com/maximhq/bifrost/core/providers/utils"

	"github.com/tidwall/gjson"
)

type ImagenReferenceImage struct {
	ReferenceType   string                 `json:"referenceType"`
	ReferenceID     int                    `json:"referenceId"`
	ReferenceImage  ImagenReferenceData    `json:"referenceImage"`
	MaskImageConfig *ImagenMaskImageConfig `json:"maskImageConfig,omitempty"`
}
type URLMetadata struct {
	RetrievedURL string `json:"retrievedUrl,omitempty"`

	URLRetrievalStatus string `json:"urlRetrievalStatus,omitempty"`
}
type GeminiImagenRequest struct {
	Instances   []ImagenInstance       `json:"instances"`
	Parameters  GeminiImagenParameters `json:"parameters"`
	ExtraParams map[string]interface{} `json:"-"`
}
type GroundingMetadataSourceFlaggingURI struct {
	FlagContentURI string `json:"flagContentUri,omitempty"`

	SourceID string `json:"sourceId,omitempty"`
}
type FunctionCall struct {
	ID string `json:"id,omitempty"`

	Args json.RawMessage `json:"args,omitempty"`

	Name string `json:"name,omitempty"`
}
type GeminiGenerationErrorStruct struct {
	Code    int                            `json:"code"`
	Message string                         `json:"message"`
	Status  string                         `json:"status"`
	Details []GeminiGenerationErrorDetails `json:"details"`
}
type AuthConfigOidcConfig struct {
	IDToken string `json:"idToken,omitempty"`

	ServiceAccount string `json:"serviceAccount,omitempty"`
}
type TrafficType string
type LogprobsResult struct {
	ChosenCandidates []*LogprobsResultCandidate `json:"chosenCandidates,omitempty"`

	TopCandidates []*LogprobsResultTopCandidates `json:"topCandidates,omitempty"`
}
type RAGRetrievalConfigRankingRankService struct {
	ModelName string `json:"modelName,omitempty"`
}
type GeminiBatchJobResponse struct {
	Name     string                `json:"name"`
	Dest     *GeminiBatchDest      `json:"dest,omitempty"`
	Error    *GeminiBatchErrorInfo `json:"error,omitempty"`
	Metadata *GeminiBatchMetadata  `json:"metadata,omitempty"`
	Done     bool                  `json:"done,omitempty"`
	Response *GeminiBatchOutput    `json:"response,omitempty"`
}
type GeminiBatchInputConfig struct {
	Requests *GeminiBatchRequestsWrapper `json:"requests,omitempty"`
	FileName string                      `json:"fileName,omitempty"`
}
type AuthType string
type ExternalAPIElasticSearchParams struct {
	Index string `json:"index,omitempty"`

	NumHits *int32 `json:"numHits,omitempty"`

	SearchTemplate string `json:"searchTemplate,omitempty"`
}
type ContentEmbeddingStatistics struct {
	TokenCount int32 `json:"tokenCount,omitempty"`
}
type PartMediaResolution struct {
	Level string `json:"level,omitempty"`

	NumTokens *int32 `json:"numTokens,omitempty"`
}
type ToolCall struct {
	ToolType string `json:"toolType,omitempty"`

	Args map[string]any `json:"args,omitempty"`

	ID string `json:"id,omitempty"`
}

var schemaIntegerJSON = sonic.Config{UseInt64: true}.Froze()

type AuthConfigGoogleServiceAccountConfig struct {
	ServiceAccount string `json:"serviceAccount,omitempty"`
}
type SafetyRating struct {
	Blocked bool `json:"blocked,omitempty"`

	Category string `json:"category,omitempty"`

	OverwrittenThreshold string `json:"overwrittenThreshold,omitempty"`

	Probability string `json:"probability,omitempty"`

	ProbabilityScore float32 `json:"probabilityScore,omitempty"`

	Severity string `json:"severity,omitempty"`

	SeverityScore float32 `json:"severityScore,omitempty"`
}
type FunctionResponse struct {
	WillContinue *bool `json:"willContinue,omitempty"`

	Scheduling string `json:"scheduling,omitempty"`

	ID string `json:"id,omitempty"`

	Name string `json:"name,omitempty"`

	Response json.RawMessage `json:"response,omitempty"`

	Parts []*Part `json:"parts,omitempty"`
}
type GeminiImagenParameters struct {
	AddWatermark            *bool                `json:"addWatermark,omitempty"`
	SampleCount             *int                 `json:"sampleCount,omitempty"`
	SampleImageSize         *string              `json:"sampleImageSize,omitempty"`
	AspectRatio             *string              `json:"aspectRatio,omitempty"`
	PersonGeneration        *string              `json:"personGeneration,omitempty"`
	Seed                    *int                 `json:"seed,omitempty"`
	NegativePrompt          *string              `json:"negativePrompt,omitempty"`
	Language                *string              `json:"language,omitempty"`
	EnhancePrompt           *bool                `json:"enhancePrompt,omitempty"`
	SafetySettings          []SafetySetting      `json:"safetySettings,omitempty"`
	OutputOptions           *ImagenOutputOptions `json:"outputOptions,omitempty"`
	BaseSteps               *int                 `json:"baseSteps,omitempty"`
	EditMode                *string              `json:"editMode,omitempty"`
	GuidanceScale           *int                 `json:"guidanceScale,omitempty"`
	IncludeRaiReason        *bool                `json:"includeRaiReason,omitempty"`
	IncludeSafetyAttributes *bool                `json:"includeSafetyAttributes,omitempty"`
	StorageUri              *string              `json:"storageUri,omitempty"`
}
type GeminiBatchEmbeddingRequest struct {
	Model       string                   `json:"-"`
	Requests    []GeminiEmbeddingRequest `json:"requests,omitempty"`
	ExtraParams map[string]interface{}   `json:"-"`
}
type GroundingChunkMapsPlaceAnswerSources struct {
	FlagContentURI string `json:"flagContentUri,omitempty"`

	ReviewSnippets []*GroundingChunkMapsPlaceAnswerSourcesReviewSnippet `json:"reviewSnippets,omitempty"`
}
type GeminiEmbeddingResponse struct {
	Embedding     *GeminiEmbedding                      `json:"embedding,omitempty"`
	Embeddings    []GeminiEmbedding                     `json:"embeddings,omitempty"`
	Metadata      *EmbedContentMetadata                 `json:"metadata,omitempty"`
	UsageMetadata *GenerateContentResponseUsageMetadata `json:"usageMetadata,omitempty"`
}
type Video struct {
	URI string `json:"uri,omitempty"`

	VideoBytes []byte `json:"videoBytes,omitempty"`

	MIMEType string `json:"mimeType,omitempty"`
}
type GeminiBatchListResponse struct {
	Operations    []GeminiBatchJobResponse `json:"operations,omitempty"`
	NextPageToken string                   `json:"nextPageToken,omitempty"`
}
type RAGRetrievalConfigRankingLlmRanker struct {
	ModelName string `json:"modelName,omitempty"`
}
type RAGRetrievalConfig struct {
	Filter *RAGRetrievalConfigFilter `json:"filter,omitempty"`

	HybridSearch *RAGRetrievalConfigHybridSearch `json:"hybridSearch,omitempty"`

	Ranking *RAGRetrievalConfigRanking `json:"ranking,omitempty"`

	TopK *int32 `json:"topK,omitempty"`
}
type GeminiVideoGenerationInstance struct {
	Prompt          string                     `json:"prompt"`
	Image           *VideoImageData            `json:"image,omitempty"`
	LastFrame       *VideoImageData            `json:"lastFrame,omitempty"`
	Mask            *VideoImageData            `json:"mask,omitempty"`
	Video           *VideoGenerationVideoInput `json:"video,omitempty"`
	ReferenceImages []VideoReferenceImage      `json:"referenceImages,omitempty"`
}
type VertexRAGStore struct {
	RAGCorpora []string `json:"ragCorpora,omitempty"`

	RAGResources []*VertexRAGStoreRAGResource `json:"ragResources,omitempty"`

	RAGRetrievalConfig *RAGRetrievalConfig `json:"ragRetrievalConfig,omitempty"`

	SimilarityTopK *int32 `json:"similarityTopK,omitempty"`

	StoreContext *bool `json:"storeContext,omitempty"`

	VectorDistanceThreshold *float64 `json:"vectorDistanceThreshold,omitempty"`
}
type ServiceTier string
type GeminiImagenPrediction struct {
	BytesBase64Encoded string `json:"bytesBase64Encoded,omitempty"`
	MimeType           string `json:"mimeType,omitempty"`
	RaiFilteredReason  string `json:"raiFilteredReason,omitempty"`
}
type GeneratedVideo struct {
	Video *Video `json:"video,omitempty"`
}
type Part struct {
	VideoMetadata *VideoMetadata `json:"videoMetadata,omitempty"`

	MediaResolution *PartMediaResolution `json:"mediaResolution,omitempty"`

	Thought bool `json:"thought,omitempty"`

	InlineData *Blob `json:"inlineData,omitempty"`

	FileData *FileData `json:"fileData,omitempty"`

	ThoughtSignature []byte `json:"thoughtSignature,omitempty"`

	CodeExecutionResult *CodeExecutionResult `json:"codeExecutionResult,omitempty"`

	ExecutableCode *ExecutableCode `json:"executableCode,omitempty"`

	FunctionCall *FunctionCall `json:"functionCall,omitempty"`

	FunctionResponse *FunctionResponse `json:"functionResponse,omitempty"`

	ToolCall *ToolCall `json:"toolCall,omitempty"`

	ToolResponse *ToolResponse `json:"toolResponse,omitempty"`

	Text string `json:"text,omitempty"`
}
type RetrievalConfig struct {
	LatLng *LatLng `json:"latLng,omitempty"`

	LanguageCode string `json:"languageCode,omitempty"`
}
type FinishReason string
type GeminiInlinedResponses struct {
	InlinedResponses []GeminiInlinedResponse `json:"inlinedResponses,omitempty"`
}
type FunctionCallingConfigMode string
type GroundingChunkMaps struct {
	PlaceAnswerSources *GroundingChunkMapsPlaceAnswerSources `json:"placeAnswerSources,omitempty"`

	PlaceID string `json:"placeId,omitempty"`

	Text string `json:"text,omitempty"`

	Title string `json:"title,omitempty"`

	URI string `json:"uri,omitempty"`
}
type SafetySetting struct {
	Method string `json:"method,omitempty"`

	Category string `json:"category,omitempty"`

	Threshold string `json:"threshold,omitempty"`
}
type ToolConfig struct {
	FunctionCallingConfig *FunctionCallingConfig `json:"functionCallingConfig,omitempty"`

	RetrievalConfig *RetrievalConfig `json:"retrievalConfig,omitempty"`

	IncludeServerSideToolInvocations *bool `json:"includeServerSideToolInvocations,omitempty"`
}
type VideoReferenceImage struct {
	Image         VideoImageData `json:"image"`
	ReferenceType string         `json:"referenceType"`
}
type SpeechConfig struct {
	VoiceConfig *VoiceConfig `json:"voice_config,omitempty"`

	MultiSpeakerVoiceConfig *MultiSpeakerVoiceConfig `json:"multiSpeakerVoiceConfig,omitempty"`

	LanguageCode string `json:"languageCode,omitempty"`
}
type GroundingMetadata struct {
	GoogleMapsWidgetContextToken string `json:"googleMapsWidgetContextToken,omitempty"`

	GroundingChunks []*GroundingChunk `json:"groundingChunks,omitempty"`

	GroundingSupports []*GroundingSupport `json:"groundingSupports,omitempty"`

	RetrievalMetadata *RetrievalMetadata `json:"retrievalMetadata,omitempty"`

	RetrievalQueries []string `json:"retrievalQueries,omitempty"`

	SearchEntryPoint *SearchEntryPoint `json:"searchEntryPoint,omitempty"`

	SourceFlaggingUris []*GroundingMetadataSourceFlaggingURI `json:"sourceFlaggingUris,omitempty"`

	WebSearchQueries []string `json:"webSearchQueries,omitempty"`

	ImageSearchQueries []string `json:"imageSearchQueries,omitempty"`
}
type GeminiImagenResponse struct {
	Predictions []GeminiImagenPrediction `json:"predictions"`
}
type URLContextMetadata struct {
	URLMetadata []*URLMetadata `json:"urlMetadata,omitempty"`
}
type VideoGenerationResponse struct {
	BytesBase64Encoded *string `json:"bytesBase64Encoded"`
	MIMEType           *string `json:"mimeType"`
	GCSURI             *string `json:"gcsUri"`
}
type GenerateVideosOperation struct {
	Name string `json:"name,omitempty"`

	Metadata json.RawMessage `json:"metadata,omitempty"`

	Done bool `json:"done,omitempty"`

	Error json.RawMessage `json:"error,omitempty"`

	Response *GenerateVideosOperationResponse `json:"response,omitempty"`
}
type FunctionDeclaration struct {
	Behavior Behavior `json:"behavior,omitempty"`

	Description string `json:"description,omitempty"`

	Name string `json:"name,omitempty"`

	Parameters *Schema `json:"parameters,omitempty"`

	ParametersJSONSchema any `json:"parametersJsonSchema,omitempty"`

	Response *Schema `json:"response,omitempty"`

	ResponseJSONSchema any `json:"responseJsonSchema,omitempty"`
}
type ToolResponse struct {
	ToolType string `json:"toolType,omitempty"`

	Response map[string]any `json:"response,omitempty"`

	ID string `json:"id,omitempty"`
}
type GeminiBatchStats struct {
	RequestCount           int `json:"requestCount"`
	PendingRequestCount    int `json:"pendingRequestCount"`
	SuccessfulRequestCount int `json:"successfulRequestCount"`
}
type EnterpriseWebSearch struct {
	ExcludeDomains []string `json:"excludeDomains,omitempty"`

	raw json.RawMessage
}
type RAGChunk struct {
	PageSpan *RAGChunkPageSpan `json:"pageSpan,omitempty"`

	Text string `json:"text,omitempty"`
}
type APISpec string
type FileData struct {
	DisplayName string `json:"displayName,omitempty"`

	FileURI string `json:"fileUri,omitempty"`

	MIMEType string `json:"mimeType,omitempty"`
}
type GeminiFileUploadHandlerReqFile struct {
	DisplayName string `json:"display_name"`
}
type GeminiCountTokensResponse struct {
	TotalTokens int32 `json:"totalTokens,omitempty"`

	CachedContentTokenCount int32 `json:"cachedContentTokenCount,omitempty"`

	PromptTokensDetails []*ModalityTokenCount `json:"promptTokensDetails,omitempty"`

	CacheTokensDetails []*ModalityTokenCount `json:"cacheTokensDetails,omitempty"`
}
type Schema struct {
	AnyOf []*Schema `json:"anyOf,omitempty"`

	Default any `json:"default,omitempty"`

	Description string `json:"description,omitempty"`

	Enum []string `json:"enum,omitempty"`

	Example any `json:"example,omitempty"`

	Format string `json:"format,omitempty"`

	Items *Schema `json:"items,omitempty"`

	MaxItems *int64 `json:"maxItems,omitempty"`

	MaxLength *int64 `json:"maxLength,omitempty"`

	MaxProperties *int64 `json:"maxProperties,omitempty"`

	Maximum *float64 `json:"maximum,omitempty"`

	MinItems *int64 `json:"minItems,omitempty"`

	MinLength *int64 `json:"minLength,omitempty"`

	MinProperties *int64 `json:"minProperties,omitempty"`

	Minimum *float64 `json:"minimum,omitempty"`

	Nullable *bool `json:"nullable,omitempty"`

	Pattern string `json:"pattern,omitempty"`

	Properties map[string]*Schema `json:"properties,omitempty"`

	PropertyOrdering []string `json:"propertyOrdering,omitempty"`

	Required []string `json:"required,omitempty"`

	Title string `json:"title,omitempty"`

	Type Type `json:"type,omitempty"`
}
type GeminiBatchCreateRequest struct {
	Model string            `json:"-"`
	Batch GeminiBatchConfig `json:"batch"`
}
type ImageSearch struct{}
type ImagenReferenceData struct {
	BytesBase64Encoded string `json:"bytesBase64Encoded"`
}
type thinkingBudgetRange struct {
	Min int
	Max int
}
type GeminiGenerationError struct {
	Error *GeminiGenerationErrorStruct `json:"error,omitempty"`
}
type Image struct {
	GCSURI string `json:"gcsUri,omitempty"`

	ImageBytes []byte `json:"imageBytes,omitempty"`

	MIMEType string `json:"mimeType,omitempty"`
}
type GeminiBatchConfig struct {
	DisplayName string                 `json:"displayName,omitempty"`
	InputConfig GeminiBatchInputConfig `json:"inputConfig"`
}
type GeminiListModelsResponse struct {
	Models        []GeminiModel `json:"models"`
	NextPageToken string        `json:"nextPageToken"`
}
type SpeakerVoiceConfig struct {
	Speaker string `json:"speaker,omitempty"`

	VoiceConfig *VoiceConfig `json:"voiceConfig,omitempty"`
}
type VoiceConfig struct {
	PrebuiltVoiceConfig *PrebuiltVoiceConfig `json:"prebuilt_voice_config,omitempty"`
}
type GeminiBatchFileResultLine struct {
	CustomID string                 `json:"custom_id,omitempty"`
	Key      string                 `json:"key,omitempty"`
	Response sonic.NoCopyRawMessage `json:"response,omitempty"`
	Error    *GeminiBatchErrorInfo  `json:"error,omitempty"`
}
type VideoMetadata struct {
	FPS *float64 `json:"fps,omitempty"`

	EndOffset time.Duration `json:"endOffset,omitempty"`

	StartOffset time.Duration `json:"startOffset,omitempty"`
}
type ToolCodeExecution struct{}
type GenerationConfigRoutingConfigAutoRoutingMode struct {
	ModelRoutingPreference string `json:"modelRoutingPreference,omitempty"`
}
type URLContext struct {
	raw json.RawMessage
}
type FunctionCallingConfig struct {
	Mode FunctionCallingConfigMode `json:"mode,omitempty"`

	AllowedFunctionNames []string `json:"allowedFunctionNames,omitempty"`
}
type SearchTypes struct {
	WebSearch *WebSearch `json:"webSearch,omitempty"`

	ImageSearch *ImageSearch `json:"imageSearch,omitempty"`
}
type GenerateContentResponse struct {
	Candidates []*Candidate `json:"candidates,omitempty"`

	CreateTime time.Time `json:"createTime,omitempty"`

	ModelVersion string `json:"modelVersion,omitempty"`

	PromptFeedback *GenerateContentResponsePromptFeedback `json:"promptFeedback,omitempty"`

	ResponseID string `json:"responseId,omitempty"`

	UsageMetadata *GenerateContentResponseUsageMetadata `json:"usageMetadata,omitempty"`
}
type Content struct {
	Parts []*Part `json:"parts,omitempty"`

	Role string `json:"role,omitempty"`
}
type VideoGenerationParameters struct {
	NegativePrompt *string `json:"negativePrompt,omitempty"`

	Image *VideoImageData `json:"image,omitempty"`

	LastFrame *VideoImageData `json:"lastFrame,omitempty"`

	ReferenceImages []VideoReferenceImage `json:"referenceImages,omitempty"`

	Video *VideoGenerationVideoInput `json:"video,omitempty"`

	AspectRatio *string `json:"aspectRatio,omitempty"`

	Resolution *string `json:"resolution,omitempty"`

	SampleCount *int `json:"sampleCount,omitempty"`

	DurationSeconds *int `json:"durationSeconds,omitempty"`

	PersonGeneration *string `json:"personGeneration,omitempty"`

	NumberOfVideos *int `json:"numberOfVideos,omitempty"`

	Seed *int `json:"seed,omitempty"`

	StorageURI *string `json:"storageURI,omitempty"`

	CompressionQuality *string `json:"compressionQuality,omitempty"`

	EnhancePrompt *bool `json:"enhancePrompt,omitempty"`

	GenerateAudio *bool `json:"generateAudio,omitempty"`

	ResizeMode *string `json:"resizeMode,omitempty"`
}
type MultiSpeakerVoiceConfig struct {
	SpeakerVoiceConfigs []*SpeakerVoiceConfig `json:"speakerVoiceConfigs,omitempty"`
}
type Segment struct {
	EndIndex int32 `json:"endIndex,omitempty"`

	PartIndex int32 `json:"partIndex,omitempty"`

	StartIndex int32 `json:"startIndex,omitempty"`

	Text string `json:"text,omitempty"`
}
type VideoGenerationVideoInput struct {
	InlineData *Blob     `json:"inlineData,omitempty"`
	FileData   *FileData `json:"fileData,omitempty"`
	URI        *string   `json:"uri,omitempty"`
}
type WebSearch struct{}
type GeminiVideoGenerationRequest struct {
	Model       string                          `json:"model,omitempty"`
	Instances   []GeminiVideoGenerationInstance `json:"instances"`
	Parameters  *VideoGenerationParameters      `json:"parameters,omitempty"`
	Fallbacks   []string                        `json:"fallbacks,omitempty"`
	ExtraParams map[string]interface{}          `json:"-"`
}
type GeminiGenerationRequest struct {
	Model             string                   `json:"model,omitempty"`
	Contents          []Content                `json:"contents,omitempty"`
	Requests          []GeminiEmbeddingRequest `json:"requests,omitempty"`
	SystemInstruction *Content                 `json:"systemInstruction,omitempty"`
	GenerationConfig  GenerationConfig         `json:"generationConfig,omitempty"`
	SafetySettings    []SafetySetting          `json:"safetySettings,omitempty"`
	Tools             []Tool                   `json:"tools,omitempty"`
	ToolConfig        *ToolConfig              `json:"toolConfig,omitempty"`
	Labels            map[string]string        `json:"labels,omitempty"`
	CachedContent     string                   `json:"cachedContent,omitempty"`
	SessionID         *GeminiSessionID         `json:"session_id,omitempty"`
	ContinuationToken string                   `json:"continuationToken,omitempty"`

	ServiceTier       ServiceTier `json:"serviceTier,omitempty"`
	Stream            bool        `json:"-"`
	IsEmbedding       bool        `json:"-"`
	IsTranscription   bool        `json:"-"`
	IsSpeech          bool        `json:"-"`
	IsImageGeneration bool        `json:"-"`
	IsImageEdit       bool        `json:"-"`
	IsCountTokens     bool        `json:"-"`

	Instances  []ImagenInstance        `json:"instances,omitempty"`
	Parameters *GeminiImagenParameters `json:"parameters,omitempty"`

	Fallbacks   []string               `json:"fallbacks,omitempty"`
	ExtraParams map[string]interface{} `json:"-"`
}
type LatLng struct {
	Latitude *float64 `json:"latitude,omitempty"`

	Longitude *float64 `json:"longitude,omitempty"`
}
type ToolComputerUse struct {
	Environment Environment `json:"environment,omitempty"`

	raw json.RawMessage
}
type Behavior string
type ImagenInstance struct {
	Prompt          string                 `json:"prompt,omitempty"`
	ReferenceImages []ImagenReferenceImage `json:"referenceImages,omitempty"`
}
type GenerationConfigRoutingConfigManualRoutingMode struct {
	ModelName string `json:"modelName,omitempty"`
}
type GeminiEmbeddingRequest struct {
	Content              *Content                  `json:"content,omitempty"`
	EmbedContentConfig   *GeminiEmbedContentConfig `json:"embedContentConfig,omitempty"`
	TaskType             *string                   `json:"taskType,omitempty"`
	Title                *string                   `json:"title,omitempty"`
	OutputDimensionality *int                      `json:"outputDimensionality,omitempty"`
	Model                string                    `json:"model,omitempty"`
	Fallbacks            []string                  `json:"fallbacks,omitempty"`
	ExtraParams          map[string]interface{}    `json:"-"`
}
type GroundingChunkRetrievedContext struct {
	DocumentName string `json:"documentName,omitempty"`

	RAGChunk *RAGChunk `json:"ragChunk,omitempty"`

	Text string `json:"text,omitempty"`

	Title string `json:"title,omitempty"`

	URI string `json:"uri,omitempty"`
}
type GroundingChunkMapsPlaceAnswerSourcesAuthorAttribution struct {
	DisplayName string `json:"displayName,omitempty"`

	PhotoURI string `json:"photoUri,omitempty"`

	URI string `json:"uri,omitempty"`
}
type Tool struct {
	FunctionDeclarations []*FunctionDeclaration `json:"functionDeclarations,omitempty"`

	Retrieval *Retrieval `json:"retrieval,omitempty"`

	GoogleSearch *GoogleSearch `json:"googleSearch,omitempty"`

	GoogleSearchRetrieval *GoogleSearchRetrieval `json:"googleSearchRetrieval,omitempty"`

	EnterpriseWebSearch *EnterpriseWebSearch `json:"enterpriseWebSearch,omitempty"`

	GoogleMaps *GoogleMaps `json:"googleMaps,omitempty"`

	URLContext *URLContext `json:"urlContext,omitempty"`

	ComputerUse *ToolComputerUse `json:"computerUse,omitempty"`

	CodeExecution *ToolCodeExecution `json:"codeExecution,omitempty"`
}
type Citation struct {
	EndIndex int32 `json:"endIndex,omitempty"`

	License string `json:"license,omitempty"`

	PublicationDate civil.Date `json:"publicationDate,omitempty"`

	StartIndex int32 `json:"startIndex,omitempty"`

	Title string `json:"title,omitempty"`

	URI string `json:"uri,omitempty"`
}
type GeminiGenerationErrorDetails struct {
	Type            string `json:"@type"`
	RetryDelay      string `json:"retryDelay,omitempty"`
	FieldViolations []struct {
		Description string `json:"description"`
	} `json:"fieldViolations"`
}
type GoogleSearchRetrieval struct {
	DynamicRetrievalConfig *DynamicRetrievalConfig `json:"dynamicRetrievalConfig,omitempty"`

	raw json.RawMessage
}
type GeminiInlinedResponse struct {
	Response *GenerateContentResponse `json:"response,omitempty"`
	Error    *GeminiBatchErrorInfo    `json:"error,omitempty"`
	Metadata *GeminiBatchMetadata     `json:"metadata,omitempty"`
}
type Type string
type GeminiBatchGenerateContentRequest struct {
	Contents          []Content         `json:"contents"`
	GenerationConfig  *GenerationConfig `json:"generationConfig,omitempty"`
	SafetySettings    []SafetySetting   `json:"safetySettings,omitempty"`
	SystemInstruction *Content          `json:"systemInstruction,omitempty"`
	Tools             []Tool            `json:"tools,omitempty"`
	ToolConfig        *ToolConfig       `json:"toolConfig,omitempty"`
	CachedContent     string            `json:"cachedContent,omitempty"`
	Labels            map[string]string `json:"labels,omitempty"`
}
type APIAuth struct {
	APIKeyConfig *APIAuthAPIKeyConfig `json:"apiKeyConfig,omitempty"`
}
type Blob struct {
	DisplayName string `json:"displayName,omitempty"`

	Data string `json:"data,omitempty"`

	MIMEType string `json:"mimeType,omitempty"`
}
type CitationMetadata struct {
	Citations []*Citation `json:"citations,omitempty"`
}
type RAGRetrievalConfigRanking struct {
	LlmRanker *RAGRetrievalConfigRankingLlmRanker `json:"llmRanker,omitempty"`

	RankService *RAGRetrievalConfigRankingRankService `json:"rankService,omitempty"`
}
type APIKeyConfig struct {
	APIKeyString string `json:"apiKeyString,omitempty"`
}
type flexibleSchemaInt64 int64
type GeminiSessionID struct {
	Value   string
	Numeric bool
}
type ExternalAPI struct {
	APIAuth *APIAuth `json:"apiAuth,omitempty"`

	APISpec APISpec `json:"apiSpec,omitempty"`

	AuthConfig *AuthConfig `json:"authConfig,omitempty"`

	ElasticSearchParams *ExternalAPIElasticSearchParams `json:"elasticSearchParams,omitempty"`

	Endpoint string `json:"endpoint,omitempty"`

	SimpleSearchParams *ExternalAPISimpleSearchParams `json:"simpleSearchParams,omitempty"`
}
type GeminiBatchDest struct {
	FileName string `json:"fileName,omitempty"`
}
type GenerateVideosOperationResponse struct {
	Type string `json:"@type,omitempty"`

	GenerateVideoResponse *GenerateVideoResponse `json:"generateVideoResponse,omitempty"`

	GeneratedVideos []*GeneratedVideo `json:"generatedVideos,omitempty"`

	Videos []VideoGenerationResponse `json:"videos,omitempty"`
}
type SearchEntryPoint struct {
	RenderedContent string `json:"renderedContent,omitempty"`

	SDKBlob []byte `json:"sdkBlob,omitempty"`
}
type EmbedContentMetadata struct {
	BillableCharacterCount int32 `json:"billableCharacterCount,omitempty"`
}
type RAGRetrievalConfigHybridSearch struct {
	Alpha *float64 `json:"alpha,omitempty"`
}
type LogprobsResultCandidate struct {
	LogProbability float32 `json:"logProbability,omitempty"`

	Token string `json:"token,omitempty"`

	TokenID int32 `json:"tokenId,omitempty"`
}
type GroundingChunkMapsPlaceAnswerSourcesReviewSnippet struct {
	AuthorAttribution *GroundingChunkMapsPlaceAnswerSourcesAuthorAttribution `json:"authorAttribution,omitempty"`

	FlagContentURI string `json:"flagContentUri,omitempty"`

	GoogleMapsURI string `json:"googleMapsUri,omitempty"`

	RelativePublishTimeDescription string `json:"relativePublishTimeDescription,omitempty"`

	Review string `json:"review,omitempty"`

	ReviewID string `json:"reviewId,omitempty"`

	Title string `json:"title,omitempty"`
}
type GroundingChunk struct {
	Maps *GroundingChunkMaps `json:"maps,omitempty"`

	Image *GroundingChunkImage `json:"image,omitempty"`

	RetrievedContext *GroundingChunkRetrievedContext `json:"retrievedContext,omitempty"`

	Web *GroundingChunkWeb `json:"web,omitempty"`
}
type AuthConfigHTTPBasicAuthConfig struct {
	CredentialSecret string `json:"credentialSecret,omitempty"`
}
type AuthConfigOauthConfig struct {
	AccessToken string `json:"accessToken,omitempty"`

	ServiceAccount string `json:"serviceAccount,omitempty"`
}
type APIAuthAPIKeyConfig struct {
	APIKeySecretVersion string `json:"apiKeySecretVersion,omitempty"`

	APIKeyString string `json:"apiKeyString,omitempty"`
}
type Role string
type GeminiEmbedding struct {
	Values     []float64                   `json:"values"`
	Statistics *ContentEmbeddingStatistics `json:"statistics,omitempty"`
}
type CodeExecutionResult struct {
	Outcome Outcome `json:"outcome,omitempty"`

	Output string `json:"output,omitempty"`
}
type Interval struct {
	StartTime time.Time `json:"startTime,omitempty"`

	EndTime time.Time `json:"endTime,omitempty"`
}
type GenerateContentResponseUsageMetadata struct {
	CacheTokensDetails []*ModalityTokenCount `json:"cacheTokensDetails,omitempty"`

	CachedContentTokenCount int32 `json:"cachedContentTokenCount,omitempty"`

	CandidatesTokenCount int32 `json:"candidatesTokenCount,omitempty"`

	CandidatesTokensDetails []*ModalityTokenCount `json:"candidatesTokensDetails,omitempty"`

	PromptTokenCount int32 `json:"promptTokenCount,omitempty"`

	PromptTokensDetails []*ModalityTokenCount `json:"promptTokensDetails,omitempty"`

	ThoughtsTokenCount int32 `json:"thoughtsTokenCount,omitempty"`

	ToolUsePromptTokenCount int32 `json:"toolUsePromptTokenCount,omitempty"`

	ToolUsePromptTokensDetails []*ModalityTokenCount `json:"toolUsePromptTokensDetails,omitempty"`

	TotalTokenCount int32 `json:"totalTokenCount,omitempty"`

	TrafficType TrafficType `json:"trafficType,omitempty"`

	ServiceTier ServiceTier `json:"serviceTier,omitempty"`
}
type GeminiCountTokensRequest struct {
	GeminiGenerationRequest
	GenerateContentRequest *GeminiGenerationRequest `json:"generateContentRequest,omitempty"`
}
type GroundingSupport struct {
	ConfidenceScores []float32 `json:"confidenceScores,omitempty"`

	GroundingChunkIndices []int32 `json:"groundingChunkIndices,omitempty"`

	Segment *Segment `json:"segment,omitempty"`
}
type Outcome string
type Retrieval struct {
	DisableAttribution bool `json:"disableAttribution,omitempty"`

	ExternalAPI *ExternalAPI `json:"externalApi,omitempty"`

	VertexAISearch *VertexAISearch `json:"vertexAiSearch,omitempty"`

	VertexRAGStore *VertexRAGStore `json:"vertexRagStore,omitempty"`

	raw json.RawMessage
}
type Candidate struct {
	Content *Content `json:"content,omitempty"`

	CitationMetadata *CitationMetadata `json:"citationMetadata,omitempty"`

	FinishMessage string `json:"finishMessage,omitempty"`

	TokenCount int32 `json:"tokenCount,omitempty"`

	FinishReason FinishReason `json:"finishReason,omitempty"`

	ContinuationToken string `json:"continuationToken,omitempty"`

	URLContextMetadata *URLContextMetadata `json:"urlContextMetadata,omitempty"`

	AvgLogprobs float64 `json:"avgLogprobs,omitempty"`

	GroundingMetadata *GroundingMetadata `json:"groundingMetadata,omitempty"`

	Index int32 `json:"index,omitempty"`

	LogprobsResult *LogprobsResult `json:"logprobsResult,omitempty"`

	SafetyRatings []*SafetyRating `json:"safetyRatings,omitempty"`
}
type GeminiBatchMetadataInputConfig struct {
	FileName string `json:"fileName,omitempty"`
}
type GenerateContentResponsePromptFeedback struct {
	BlockReason string `json:"blockReason,omitempty"`

	BlockReasonMessage string `json:"blockReasonMessage,omitempty"`

	SafetyRatings []*SafetyRating `json:"safetyRatings,omitempty"`
}
type LogprobsResultTopCandidates struct {
	Candidates []*LogprobsResultCandidate `json:"candidates,omitempty"`
}
type ImagenOutputOptions struct {
	MimeType           *string `json:"mimeType,omitempty"`
	CompressionQuality *int    `json:"compressionQuality,omitempty"`
}
type ExecutableCode struct {
	Code string `json:"code,omitempty"`

	Language string `json:"language,omitempty"`
}
type VideoImageData struct {
	BytesBase64Encoded *string   `json:"bytesBase64Encoded,omitempty"`
	GCSURI             *string   `json:"gcsUri,omitempty"`
	FileData           *FileData `json:"fileData,omitempty"`
	MimeType           *string   `json:"mimeType,omitempty"`
	MaskMode           *string   `json:"maskMode,omitempty"`
}
type GoogleSearch struct {
	TimeRangeFilter *Interval `json:"timeRangeFilter,omitempty"`

	ExcludeDomains []string `json:"excludeDomains,omitempty"`

	SearchTypes *SearchTypes `json:"searchTypes,omitempty"`
}
type RAGRetrievalConfigFilter struct {
	MetadataFilter string `json:"metadataFilter,omitempty"`

	VectorDistanceThreshold *float64 `json:"vectorDistanceThreshold,omitempty"`

	VectorSimilarityThreshold *float64 `json:"vectorSimilarityThreshold,omitempty"`
}
type AuthConfig struct {
	APIKeyConfig *APIKeyConfig `json:"apiKeyConfig,omitempty"`

	AuthType AuthType `json:"authType,omitempty"`

	GoogleServiceAccountConfig *AuthConfigGoogleServiceAccountConfig `json:"googleServiceAccountConfig,omitempty"`

	HTTPBasicAuthConfig *AuthConfigHTTPBasicAuthConfig `json:"httpBasicAuthConfig,omitempty"`

	OauthConfig *AuthConfigOauthConfig `json:"oauthConfig,omitempty"`

	OidcConfig *AuthConfigOidcConfig `json:"oidcConfig,omitempty"`
}
type GenerationConfigThinkingConfig struct {
	IncludeThoughts bool `json:"includeThoughts,omitempty"`

	ThinkingBudget *int32 `json:"thinkingBudget,omitempty"`

	ThinkingLevel *string `json:"thinkingLevel,omitempty"`
}
type GenerateVideoResponse struct {
	GeneratedSamples []*GeneratedVideo `json:"generatedSamples,omitempty"`

	RAIMediaFilteredCount int32 `json:"raiMediaFilteredCount,omitempty"`

	RAIMediaFilteredReasons []string `json:"raiMediaFilteredReasons,omitempty"`
}
type GeminiBatchRequestsWrapper struct {
	Requests []GeminiBatchRequestItem `json:"requests"`
}
type VertexRAGStoreRAGResource struct {
	RAGCorpus string `json:"ragCorpus,omitempty"`

	RAGFileIDs []string `json:"ragFileIds,omitempty"`
}
type GenerationConfig struct {
	ModelSelectionConfig *ModelSelectionConfig `json:"modelSelectionConfig,omitempty"`

	AudioTimestamp bool `json:"audioTimestamp,omitempty"`

	CandidateCount int32 `json:"candidateCount,omitempty"`

	EnableAffectiveDialog *bool `json:"enableAffectiveDialog,omitempty"`

	FrequencyPenalty *float64 `json:"frequencyPenalty,omitempty"`

	Logprobs *int32 `json:"logprobs,omitempty"`

	MaxOutputTokens int32 `json:"maxOutputTokens,omitempty"`

	MediaResolution string `json:"mediaResolution,omitempty"`

	PresencePenalty *float64 `json:"presencePenalty,omitempty"`

	ResponseJSONSchema any `json:"responseJsonSchema,omitempty"`

	ResponseLogprobs bool `json:"responseLogprobs,omitempty"`

	ResponseMIMEType string `json:"responseMimeType,omitempty"`

	ResponseModalities []Modality `json:"responseModalities,omitempty"`

	ResponseSchema *Schema `json:"responseSchema,omitempty"`

	RoutingConfig *GenerationConfigRoutingConfig `json:"routingConfig,omitempty"`

	Seed *int32 `json:"seed,omitempty"`

	SpeechConfig *SpeechConfig `json:"speechConfig,omitempty"`

	StopSequences []string `json:"stopSequences,omitempty"`

	Temperature *float64 `json:"temperature,omitempty"`

	ThinkingConfig *GenerationConfigThinkingConfig `json:"thinkingConfig,omitempty"`

	TopK *int `json:"topK,omitempty"`

	TopP *float64 `json:"topP,omitempty"`

	ImageConfig *GeminiImageConfig `json:"imageConfig,omitempty"`
}
type DynamicRetrievalConfig struct {
	Mode string `json:"mode,omitempty"`

	DynamicThreshold *float32 `json:"dynamicThreshold,omitempty"`
}
type VertexAISearch struct {
	DataStoreSpecs []*VertexAISearchDataStoreSpec `json:"dataStoreSpecs,omitempty"`

	Datastore string `json:"datastore,omitempty"`

	Engine string `json:"engine,omitempty"`

	Filter string `json:"filter,omitempty"`

	MaxResults *int32 `json:"maxResults,omitempty"`
}

var geminiServerSideSearchToolTypes = map[string]bool{
	"GOOGLE_SEARCH_WEB":   true,
	"GOOGLE_SEARCH_IMAGE": true,
}

type dateJSON civil.Date
type GeminiEmbedContentConfig struct {
	DocumentOCR          *bool `json:"documentOcr,omitempty"`
	AudioTrackExtraction *bool `json:"audioTrackExtraction,omitempty"`
}
type GeminiFileResponseLine struct {
	StatusCode int                    `json:"status_code"`
	Body       map[string]interface{} `json:"body"`
}
type GeminiBatchMetadata struct {
	Key         string                           `json:"key"`
	Type        string                           `json:"@type"`
	Model       string                           `json:"model"`
	DisplayName string                           `json:"displayName"`
	InputConfig *GeminiBatchMetadataInputConfig  `json:"inputConfig,omitempty"`
	Output      *GeminiBatchMetadataOutputConfig `json:"output,omitempty"`
	CreateTime  string                           `json:"createTime"`
	EndTime     string                           `json:"endTime,omitempty"`
	UpdateTime  string                           `json:"updateTime"`
	BatchStats  *GeminiBatchStats                `json:"batchStats"`
	State       string                           `json:"state"`
	Name        string                           `json:"name"`
}
type GenerationConfigRoutingConfig struct {
	AutoMode *GenerationConfigRoutingConfigAutoRoutingMode `json:"autoMode,omitempty"`

	ManualMode *GenerationConfigRoutingConfigManualRoutingMode `json:"manualMode,omitempty"`
}
type RAGChunkPageSpan struct {
	FirstPage int32 `json:"firstPage,omitempty"`

	LastPage int32 `json:"lastPage,omitempty"`
}
type ExternalAPISimpleSearchParams struct{}
type GeminiImageConfig struct {
	AspectRatio string `json:"aspectRatio,omitempty"`

	ImageSize string `json:"imageSize,omitempty"`
}
type Environment string
type GeminiBatchOutput struct {
	Type             string                  `json:"@type,omitempty"`
	ResponsesFile    string                  `json:"responsesFile,omitempty"`
	InlinedResponses *GeminiInlinedResponses `json:"inlinedResponses,omitempty"`
}
type GroundingChunkImage struct {
	SourceURI string `json:"sourceUri,omitempty"`

	ImageURI string `json:"imageUri,omitempty"`

	Title string `json:"title,omitempty"`

	Domain string `json:"domain,omitempty"`
}
type ModelSelectionConfig struct {
	FeatureSelectionPreference string `json:"featureSelectionPreference,omitempty"`
}
type PrebuiltVoiceConfig struct {
	VoiceName string `json:"voice_name,omitempty"`
}
type GoogleMaps struct {
	AuthConfig *AuthConfig `json:"authConfig,omitempty"`

	raw json.RawMessage
}
type GeminiBatchRequestItem struct {
	Request  GeminiBatchGenerateContentRequest `json:"request"`
	Metadata *GeminiBatchMetadata              `json:"metadata,omitempty"`
}
type GeminiBatchMetadataOutputConfig struct {
	ResponsesFile    string                  `json:"responsesFile,omitempty"`
	InlinedResponses *GeminiInlinedResponses `json:"inlinedResponses,omitempty"`
}
type GeminiBatchErrorInfo struct {
	Code    int    `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
	Status  string `json:"status,omitempty"`
}
type Modality string
type GeminiModel struct {
	Name                       string   `json:"name"`
	BaseModelID                string   `json:"baseModelId"`
	Version                    string   `json:"version"`
	DisplayName                string   `json:"displayName"`
	Description                string   `json:"description"`
	InputTokenLimit            int      `json:"inputTokenLimit"`
	OutputTokenLimit           int      `json:"outputTokenLimit"`
	SupportedGenerationMethods []string `json:"supportedGenerationMethods"`
	Thinking                   bool     `json:"thinking"`
	Temperature                float64  `json:"temperature"`
	MaxTemperature             float64  `json:"maxTemperature"`
	TopP                       float64  `json:"topP"`
	TopK                       int      `json:"topK"`
}
type RetrievalMetadata struct {
	GoogleSearchDynamicRetrievalScore float32 `json:"googleSearchDynamicRetrievalScore,omitempty"`
}
type ImagenMaskImageConfig struct {
	MaskMode    string   `json:"maskMode"`
	Dilation    *float64 `json:"dilation,omitempty"`
	MaskClasses []int    `json:"maskClasses,omitempty"`
}
type VertexAISearchDataStoreSpec struct {
	DataStore string `json:"dataStore,omitempty"`

	Filter string `json:"filter,omitempty"`
}
type ModalityTokenCount struct {
	Modality Modality `json:"modality,omitempty"`

	TokenCount int32 `json:"tokenCount"`
}
type GroundingChunkWeb struct {
	Domain string `json:"domain,omitempty"`

	Title string `json:"title,omitempty"`

	URI string `json:"uri,omitempty"`
}

func hasJSONKey(data []byte, key string) bool {
	return gjson.GetBytes(data, key).Exists()
}

const (
	MinReasoningMaxTokens         = 1
	DefaultCompletionMaxTokens    = 8192
	DefaultReasoningMinBudget     = 1024
	DynamicReasoningBudget        = -1
	skipThoughtSignatureValidator = "skip_thought_signature_validator"
)

const thoughtSignatureSeparator = providerUtils.ThoughtSignatureSeparator

const (
	RoleUser  = "user"
	RoleModel = "model"
)

const (
	FinishReasonUnspecified FinishReason = "FINISH_REASON_UNSPECIFIED"

	FinishReasonStop FinishReason = "STOP"

	FinishReasonMaxTokens FinishReason = "MAX_TOKENS"

	FinishReasonSafety FinishReason = "SAFETY"

	FinishReasonRecitation FinishReason = "RECITATION"

	FinishReasonLanguage FinishReason = "LANGUAGE"

	FinishReasonOther FinishReason = "OTHER"

	FinishReasonBlocklist FinishReason = "BLOCKLIST"

	FinishReasonProhibitedContent FinishReason = "PROHIBITED_CONTENT"

	FinishReasonSPII FinishReason = "SPII"

	FinishReasonMalformedFunctionCall FinishReason = "MALFORMED_FUNCTION_CALL"

	FinishReasonImageSafety FinishReason = "IMAGE_SAFETY"

	FinishReasonUnexpectedToolCall FinishReason = "UNEXPECTED_TOOL_CALL"

	FinishReasonImageProhibitedContent FinishReason = "IMAGE_PROHIBITED_CONTENT"

	FinishReasonImageOther FinishReason = "IMAGE_OTHER"

	FinishReasonNoImage FinishReason = "NO_IMAGE"

	FinishReasonImageRecitation FinishReason = "IMAGE_RECITATION"

	FinishReasonTooManyToolCalls FinishReason = "TOO_MANY_TOOL_CALLS"

	FinishReasonMissingThoughtSignature FinishReason = "MISSING_THOUGHT_SIGNATURE"

	FinishReasonMalformedResponse FinishReason = "MALFORMED_RESPONSE"

	FinishReasonContinuation FinishReason = "CONTINUATION"
)

const googleRetryInfoType = "type.googleapis.com/google.rpc.RetryInfo"

func (g *GeminiGenerationRequest) UnmarshalJSON(data []byte) error {
	type Alias GeminiGenerationRequest
	aux := &struct {
		*Alias

		SystemInstructionSnake *Content          `json:"system_instruction,omitempty"`
		GenerationConfigSnake  *GenerationConfig `json:"generation_config,omitempty"`
		SafetySettingsSnake    []SafetySetting   `json:"safety_settings,omitempty"`
		ToolConfigSnake        *ToolConfig       `json:"tool_config,omitempty"`
		CachedContentSnake     string            `json:"cached_content,omitempty"`
		ServiceTierSnake       ServiceTier       `json:"service_tier,omitempty"`
		ContinuationTokenSnake string            `json:"continuation_token,omitempty"`
	}{
		Alias: (*Alias)(g),
	}

	if err := sonic.Unmarshal(data, aux); err != nil {
		return err
	}

	if !hasJSONKey(data, "systemInstruction") && aux.SystemInstructionSnake != nil {
		g.SystemInstruction = aux.SystemInstructionSnake
	}
	if !hasJSONKey(data, "generationConfig") && aux.GenerationConfigSnake != nil {
		g.GenerationConfig = *aux.GenerationConfigSnake
	}
	if !hasJSONKey(data, "safetySettings") && len(aux.SafetySettingsSnake) > 0 {
		g.SafetySettings = aux.SafetySettingsSnake
	}
	if !hasJSONKey(data, "toolConfig") && aux.ToolConfigSnake != nil {
		g.ToolConfig = aux.ToolConfigSnake
	}
	if !hasJSONKey(data, "cachedContent") && aux.CachedContentSnake != "" {
		g.CachedContent = aux.CachedContentSnake
	}
	if !hasJSONKey(data, "serviceTier") && aux.ServiceTierSnake != "" {
		g.ServiceTier = aux.ServiceTierSnake
	}
	if !hasJSONKey(data, "continuationToken") && aux.ContinuationTokenSnake != "" {
		g.ContinuationToken = aux.ContinuationTokenSnake
	}

	return nil
}

func (r *GeminiGenerationRequest) GetExtraParams() map[string]interface{} {
	return r.ExtraParams
}

func (s GeminiSessionID) MarshalJSON() ([]byte, error) {
	if s.Numeric {
		return []byte(s.Value), nil
	}
	return sonic.Marshal(s.Value)
}

func (s *GeminiSessionID) UnmarshalJSON(data []byte) error {
	switch v := gjson.ParseBytes(data); v.Type {
	case gjson.String:
		*s = GeminiSessionID{Value: v.Str}
	case gjson.Number:
		*s = GeminiSessionID{Value: v.Raw, Numeric: true}
	case gjson.Null:
		*s = GeminiSessionID{}
	default:
		return fmt.Errorf("session_id must be a string or a number, got %s", v.Raw)
	}
	return nil
}

func SafeExtractSafetySettings(value interface{}) ([]SafetySetting, bool) {
	if value == nil {
		return nil, false
	}
	switch v := value.(type) {
	case []SafetySetting:
		return v, true
	case []interface{}:
		settings := make([]SafetySetting, 0, len(v))
		for _, item := range v {
			if m, ok := item.(map[string]interface{}); ok {
				setting := SafetySetting{}
				if method, ok := m["method"].(string); ok {
					setting.Method = method
				}
				if category, ok := m["category"].(string); ok {
					setting.Category = category
				}
				if threshold, ok := m["threshold"].(string); ok {
					setting.Threshold = threshold
				}
				settings = append(settings, setting)
			} else {
				return nil, false
			}
		}
		return settings, true
	default:
		return nil, false
	}
}

func (f *FunctionCallingConfig) UnmarshalJSON(data []byte) error {
	type Alias FunctionCallingConfig
	aux := &struct {
		*Alias
		AllowedFunctionNamesSnake []string `json:"allowed_function_names,omitempty"`
	}{
		Alias: (*Alias)(f),
	}

	if err := sonic.Unmarshal(data, aux); err != nil {
		return err
	}

	if f.AllowedFunctionNames == nil && aux.AllowedFunctionNamesSnake != nil {
		f.AllowedFunctionNames = aux.AllowedFunctionNamesSnake
	}

	return nil
}

const (
	FunctionCallingConfigModeUnspecified FunctionCallingConfigMode = "MODE_UNSPECIFIED"

	FunctionCallingConfigModeAuto FunctionCallingConfigMode = "AUTO"

	FunctionCallingConfigModeAny FunctionCallingConfigMode = "ANY"

	FunctionCallingConfigModeNone FunctionCallingConfigMode = "NONE"

	FunctionCallingConfigModeValidated FunctionCallingConfigMode = "VALIDATED"
)

func (t *ToolConfig) UnmarshalJSON(data []byte) error {
	type Alias ToolConfig
	aux := &struct {
		*Alias

		FunctionCallingConfigSnake            *FunctionCallingConfig `json:"function_calling_config,omitempty"`
		RetrievalConfigSnake                  *RetrievalConfig       `json:"retrieval_config,omitempty"`
		IncludeServerSideToolInvocationsSnake *bool                  `json:"include_server_side_tool_invocations,omitempty"`
	}{
		Alias: (*Alias)(t),
	}

	if err := sonic.Unmarshal(data, aux); err != nil {
		return err
	}

	if t.FunctionCallingConfig == nil && aux.FunctionCallingConfigSnake != nil {
		t.FunctionCallingConfig = aux.FunctionCallingConfigSnake
	}
	if t.RetrievalConfig == nil && aux.RetrievalConfigSnake != nil {
		t.RetrievalConfig = aux.RetrievalConfigSnake
	}
	if t.IncludeServerSideToolInvocations == nil && aux.IncludeServerSideToolInvocationsSnake != nil {
		t.IncludeServerSideToolInvocations = aux.IncludeServerSideToolInvocationsSnake
	}

	return nil
}

func (i *Interval) UnmarshalJSON(data []byte) error {

	type Alias Interval
	aux := &struct {
		StartTime *time.Time `json:"startTime,omitempty"`
		EndTime   *time.Time `json:"endTime,omitempty"`

		StartTimeSnake *time.Time `json:"start_time,omitempty"`
		EndTimeSnake   *time.Time `json:"end_time,omitempty"`
		*Alias
	}{
		Alias: (*Alias)(i),
	}

	if err := sonic.Unmarshal(data, &aux); err != nil {
		return err
	}

	if !reflect.ValueOf(aux.StartTime).IsZero() {
		i.StartTime = time.Time(*aux.StartTime)
	} else if !reflect.ValueOf(aux.StartTimeSnake).IsZero() {
		i.StartTime = time.Time(*aux.StartTimeSnake)
	}

	if !reflect.ValueOf(aux.EndTime).IsZero() {
		i.EndTime = time.Time(*aux.EndTime)
	} else if !reflect.ValueOf(aux.EndTimeSnake).IsZero() {
		i.EndTime = time.Time(*aux.EndTimeSnake)
	}

	return nil
}

func (i *Interval) MarshalJSON() ([]byte, error) {
	type Alias Interval
	aux := &struct {
		StartTime *time.Time `json:"startTime,omitempty"`
		EndTime   *time.Time `json:"endTime,omitempty"`
		*Alias
	}{
		Alias: (*Alias)(i),
	}

	if !reflect.ValueOf(i.StartTime).IsZero() {
		aux.StartTime = (*time.Time)(&i.StartTime)
	}

	if !reflect.ValueOf(i.EndTime).IsZero() {
		aux.EndTime = (*time.Time)(&i.EndTime)
	}

	return providerUtils.MarshalSorted(aux)
}

func (s *SearchTypes) UnmarshalJSON(data []byte) error {
	type Alias SearchTypes
	aux := &struct {
		*Alias

		WebSearchSnake   *WebSearch   `json:"web_search,omitempty"`
		ImageSearchSnake *ImageSearch `json:"image_search,omitempty"`
	}{
		Alias: (*Alias)(s),
	}

	if err := sonic.Unmarshal(data, aux); err != nil {
		return err
	}

	if s.WebSearch == nil && aux.WebSearchSnake != nil {
		s.WebSearch = aux.WebSearchSnake
	}
	if s.ImageSearch == nil && aux.ImageSearchSnake != nil {
		s.ImageSearch = aux.ImageSearchSnake
	}

	return nil
}

func (g *GoogleSearch) UnmarshalJSON(data []byte) error {
	type Alias GoogleSearch
	aux := &struct {
		*Alias

		TimeRangeFilterSnake *Interval    `json:"time_range_filter,omitempty"`
		ExcludeDomainsSnake  []string     `json:"exclude_domains,omitempty"`
		SearchTypesSnake     *SearchTypes `json:"search_types,omitempty"`
	}{
		Alias: (*Alias)(g),
	}

	if err := sonic.Unmarshal(data, aux); err != nil {
		return err
	}

	if g.TimeRangeFilter == nil && aux.TimeRangeFilterSnake != nil {
		g.TimeRangeFilter = aux.TimeRangeFilterSnake
	}
	if len(g.ExcludeDomains) == 0 && len(aux.ExcludeDomainsSnake) > 0 {
		g.ExcludeDomains = aux.ExcludeDomainsSnake
	}
	if g.SearchTypes == nil && aux.SearchTypesSnake != nil {
		g.SearchTypes = aux.SearchTypesSnake
	}

	return nil
}

func nativeToolJSON(data []byte) json.RawMessage {
	if len(data) == 0 || string(data) == "null" {
		return nil
	}
	return append(json.RawMessage(nil), data...)
}

func (r *Retrieval) UnmarshalJSON(data []byte) error {
	type alias Retrieval
	if err := sonic.Unmarshal(data, (*alias)(r)); err != nil {
		return err
	}
	r.raw = nativeToolJSON(data)
	return nil
}

func (r Retrieval) MarshalJSON() ([]byte, error) {
	if len(r.raw) > 0 {
		return r.raw, nil
	}
	type alias Retrieval
	return providerUtils.MarshalSorted(alias(r))
}

func (g *GoogleSearchRetrieval) UnmarshalJSON(data []byte) error {
	type alias GoogleSearchRetrieval
	if err := sonic.Unmarshal(data, (*alias)(g)); err != nil {
		return err
	}
	g.raw = nativeToolJSON(data)
	return nil
}

func (g GoogleSearchRetrieval) MarshalJSON() ([]byte, error) {
	if len(g.raw) > 0 {
		return g.raw, nil
	}
	type alias GoogleSearchRetrieval
	return providerUtils.MarshalSorted(alias(g))
}

func (e *EnterpriseWebSearch) UnmarshalJSON(data []byte) error {
	type alias EnterpriseWebSearch
	if err := sonic.Unmarshal(data, (*alias)(e)); err != nil {
		return err
	}
	e.raw = nativeToolJSON(data)
	return nil
}

func (e EnterpriseWebSearch) MarshalJSON() ([]byte, error) {
	if len(e.raw) > 0 {
		return e.raw, nil
	}
	type alias EnterpriseWebSearch
	return providerUtils.MarshalSorted(alias(e))
}

func (g *GoogleMaps) UnmarshalJSON(data []byte) error {
	type alias GoogleMaps
	if err := sonic.Unmarshal(data, (*alias)(g)); err != nil {
		return err
	}
	g.raw = nativeToolJSON(data)
	return nil
}

func (g GoogleMaps) MarshalJSON() ([]byte, error) {
	if len(g.raw) > 0 {
		return g.raw, nil
	}
	type alias GoogleMaps
	return providerUtils.MarshalSorted(alias(g))
}

func (u *URLContext) UnmarshalJSON(data []byte) error {
	type alias URLContext
	if err := sonic.Unmarshal(data, (*alias)(u)); err != nil {
		return err
	}
	u.raw = nativeToolJSON(data)
	return nil
}

func (u URLContext) MarshalJSON() ([]byte, error) {
	if len(u.raw) > 0 {
		return u.raw, nil
	}
	return []byte("{}"), nil
}

func (c *ToolComputerUse) UnmarshalJSON(data []byte) error {
	type alias ToolComputerUse
	if err := sonic.Unmarshal(data, (*alias)(c)); err != nil {
		return err
	}
	c.raw = nativeToolJSON(data)
	return nil
}

func (c ToolComputerUse) MarshalJSON() ([]byte, error) {
	if len(c.raw) > 0 {
		return c.raw, nil
	}
	type alias ToolComputerUse
	return providerUtils.MarshalSorted(alias(c))
}

func (t *Tool) UnmarshalJSON(data []byte) error {
	type Alias Tool
	aux := &struct {
		*Alias

		FunctionDeclarationsSnake  []*FunctionDeclaration `json:"function_declarations,omitempty"`
		GoogleSearchSnake          *GoogleSearch          `json:"google_search,omitempty"`
		GoogleSearchRetrievalSnake *GoogleSearchRetrieval `json:"google_search_retrieval,omitempty"`
		EnterpriseWebSearchSnake   *EnterpriseWebSearch   `json:"enterprise_web_search,omitempty"`
		GoogleMapsSnake            *GoogleMaps            `json:"google_maps,omitempty"`
		URLContextSnake            *URLContext            `json:"url_context,omitempty"`
		ComputerUseSnake           *ToolComputerUse       `json:"computer_use,omitempty"`
		CodeExecutionSnake         *ToolCodeExecution     `json:"code_execution,omitempty"`
	}{
		Alias: (*Alias)(t),
	}

	if err := sonic.Unmarshal(data, aux); err != nil {
		return err
	}

	if t.FunctionDeclarations == nil && aux.FunctionDeclarationsSnake != nil {
		t.FunctionDeclarations = aux.FunctionDeclarationsSnake
	}
	if t.GoogleSearch == nil && aux.GoogleSearchSnake != nil {
		t.GoogleSearch = aux.GoogleSearchSnake
	}
	if t.GoogleSearchRetrieval == nil && aux.GoogleSearchRetrievalSnake != nil {
		t.GoogleSearchRetrieval = aux.GoogleSearchRetrievalSnake
	}
	if t.EnterpriseWebSearch == nil && aux.EnterpriseWebSearchSnake != nil {
		t.EnterpriseWebSearch = aux.EnterpriseWebSearchSnake
	}
	if t.GoogleMaps == nil && aux.GoogleMapsSnake != nil {
		t.GoogleMaps = aux.GoogleMapsSnake
	}
	if t.URLContext == nil && aux.URLContextSnake != nil {
		t.URLContext = aux.URLContextSnake
	}
	if t.ComputerUse == nil && aux.ComputerUseSnake != nil {
		t.ComputerUse = aux.ComputerUseSnake
	}
	if t.CodeExecution == nil && aux.CodeExecutionSnake != nil {
		t.CodeExecution = aux.CodeExecutionSnake
	}

	return nil
}

const (
	ServiceTierUnspecified ServiceTier = "unspecified"

	ServiceTierFlex ServiceTier = "flex"

	ServiceTierStandard ServiceTier = "standard"

	ServiceTierPriority ServiceTier = "priority"
)

const (
	TrafficTypeUnspecified           TrafficType = "TRAFFIC_TYPE_UNSPECIFIED"
	TrafficTypeOnDemand              TrafficType = "ON_DEMAND"
	TrafficTypeOnDemandPriority      TrafficType = "ON_DEMAND_PRIORITY"
	TrafficTypeOnDemandFlex          TrafficType = "ON_DEMAND_FLEX"
	TrafficTypeProvisionedThroughput TrafficType = "PROVISIONED_THROUGHPUT"
)

func (g *GenerationConfig) UnmarshalJSON(data []byte) error {
	type Alias GenerationConfig
	aux := &struct {
		*Alias
		AudioTimestampSnake       bool                            `json:"audio_timestamp,omitempty"`
		CandidateCountSnake       int32                           `json:"candidate_count,omitempty"`
		EnableAffectiveDialogSnak *bool                           `json:"enable_affective_dialog,omitempty"`
		FrequencyPenaltySnake     *float64                        `json:"frequency_penalty,omitempty"`
		ImageConfigSnake          *GeminiImageConfig              `json:"image_config,omitempty"`
		MaxOutputTokensSnake      int32                           `json:"max_output_tokens,omitempty"`
		MediaResolutionSnake      string                          `json:"media_resolution,omitempty"`
		ModelSelectionConfigSnake *ModelSelectionConfig           `json:"model_selection_config,omitempty"`
		PresencePenaltySnake      *float64                        `json:"presence_penalty,omitempty"`
		ResponseJsonSchemaSnake   any                             `json:"response_json_schema,omitempty"`
		ResponseLogprobsSnake     bool                            `json:"response_logprobs,omitempty"`
		ResponseMIMETypeSnake     string                          `json:"response_mime_type,omitempty"`
		ResponseModalitiesSnake   []Modality                      `json:"response_modalities,omitempty"`
		ResponseSchemaSnake       *Schema                         `json:"response_schema,omitempty"`
		RoutingConfigSnake        *GenerationConfigRoutingConfig  `json:"routing_config,omitempty"`
		SpeechConfigSnake         *SpeechConfig                   `json:"speech_config,omitempty"`
		StopSequencesSnake        []string                        `json:"stop_sequences,omitempty"`
		ThinkingConfigSnake       *GenerationConfigThinkingConfig `json:"thinking_config,omitempty"`
		TopKSnake                 *int                            `json:"top_k,omitempty"`
		TopPSnake                 *float64                        `json:"top_p,omitempty"`
	}{
		Alias: (*Alias)(g),
	}

	if err := sonic.Unmarshal(data, aux); err != nil {
		return err
	}

	if !hasJSONKey(data, "audioTimestamp") && aux.AudioTimestampSnake {
		g.AudioTimestamp = aux.AudioTimestampSnake
	}
	if !hasJSONKey(data, "candidateCount") && aux.CandidateCountSnake != 0 {
		g.CandidateCount = aux.CandidateCountSnake
	}
	if !hasJSONKey(data, "enableAffectiveDialog") && aux.EnableAffectiveDialogSnak != nil {
		g.EnableAffectiveDialog = aux.EnableAffectiveDialogSnak
	}
	if !hasJSONKey(data, "frequencyPenalty") && aux.FrequencyPenaltySnake != nil {
		g.FrequencyPenalty = aux.FrequencyPenaltySnake
	}
	if !hasJSONKey(data, "imageConfig") && aux.ImageConfigSnake != nil {
		g.ImageConfig = aux.ImageConfigSnake
	}
	if !hasJSONKey(data, "maxOutputTokens") && aux.MaxOutputTokensSnake != 0 {
		g.MaxOutputTokens = aux.MaxOutputTokensSnake
	}
	if !hasJSONKey(data, "mediaResolution") && aux.MediaResolutionSnake != "" {
		g.MediaResolution = aux.MediaResolutionSnake
	}
	if !hasJSONKey(data, "modelSelectionConfig") && aux.ModelSelectionConfigSnake != nil {
		g.ModelSelectionConfig = aux.ModelSelectionConfigSnake
	}
	if !hasJSONKey(data, "presencePenalty") && aux.PresencePenaltySnake != nil {
		g.PresencePenalty = aux.PresencePenaltySnake
	}
	if !hasJSONKey(data, "responseJsonSchema") && aux.ResponseJsonSchemaSnake != nil {
		g.ResponseJSONSchema = aux.ResponseJsonSchemaSnake
	}
	if !hasJSONKey(data, "responseLogprobs") && aux.ResponseLogprobsSnake {
		g.ResponseLogprobs = aux.ResponseLogprobsSnake
	}

	if !hasJSONKey(data, "responseMimeType") && aux.ResponseMIMETypeSnake != "" {
		g.ResponseMIMEType = aux.ResponseMIMETypeSnake
	}
	if !hasJSONKey(data, "responseModalities") && aux.ResponseModalitiesSnake != nil {
		g.ResponseModalities = aux.ResponseModalitiesSnake
	}
	if !hasJSONKey(data, "responseSchema") && aux.ResponseSchemaSnake != nil {
		g.ResponseSchema = aux.ResponseSchemaSnake
	}
	if !hasJSONKey(data, "routingConfig") && aux.RoutingConfigSnake != nil {
		g.RoutingConfig = aux.RoutingConfigSnake
	}
	if !hasJSONKey(data, "speechConfig") && aux.SpeechConfigSnake != nil {
		g.SpeechConfig = aux.SpeechConfigSnake
	}
	if !hasJSONKey(data, "stopSequences") && aux.StopSequencesSnake != nil {
		g.StopSequences = aux.StopSequencesSnake
	}
	if !hasJSONKey(data, "thinkingConfig") && aux.ThinkingConfigSnake != nil {
		g.ThinkingConfig = aux.ThinkingConfigSnake
	}
	if !hasJSONKey(data, "topK") && aux.TopKSnake != nil {
		g.TopK = aux.TopKSnake
	}
	if !hasJSONKey(data, "topP") && aux.TopPSnake != nil {
		g.TopP = aux.TopPSnake
	}

	return nil
}

const (
	ModalityUnspecified Modality = "MODALITY_UNSPECIFIED"

	ModalityText Modality = "TEXT"

	ModalityImage Modality = "IMAGE"

	ModalityAudio Modality = "AUDIO"
)

func (i *flexibleSchemaInt64) UnmarshalJSON(data []byte) error {
	var value any
	if err := schemaIntegerJSON.Unmarshal(data, &value); err != nil {
		return fmt.Errorf("invalid schema integer constraint: %w", err)
	}

	switch typedValue := value.(type) {
	case nil:
		return nil
	case int64:
		*i = flexibleSchemaInt64(typedValue)
		return nil
	case string:
		parsed, err := strconv.ParseInt(typedValue, 10, 64)
		if err != nil {
			return fmt.Errorf("invalid schema integer constraint %q: %w", typedValue, err)
		}
		*i = flexibleSchemaInt64(parsed)
		return nil
	default:
		return fmt.Errorf("invalid schema integer constraint %v", typedValue)
	}
}

func schemaInt64Ptr(value *flexibleSchemaInt64) *int64 {
	if value == nil {
		return nil
	}
	out := int64(*value)
	return &out
}

func (s *Schema) UnmarshalJSON(data []byte) error {
	type schemaAlias Schema
	type schemaWithFlexibleConstraints struct {
		*schemaAlias
		MaxItems      *flexibleSchemaInt64 `json:"maxItems,omitempty"`
		MaxLength     *flexibleSchemaInt64 `json:"maxLength,omitempty"`
		MaxProperties *flexibleSchemaInt64 `json:"maxProperties,omitempty"`
		MinItems      *flexibleSchemaInt64 `json:"minItems,omitempty"`
		MinLength     *flexibleSchemaInt64 `json:"minLength,omitempty"`
		MinProperties *flexibleSchemaInt64 `json:"minProperties,omitempty"`
	}

	var aux schemaAlias
	withConstraints := schemaWithFlexibleConstraints{schemaAlias: &aux}
	if err := sonic.Unmarshal(data, &withConstraints); err != nil {
		return err
	}

	*s = Schema(aux)
	s.MaxItems = schemaInt64Ptr(withConstraints.MaxItems)
	s.MaxLength = schemaInt64Ptr(withConstraints.MaxLength)
	s.MaxProperties = schemaInt64Ptr(withConstraints.MaxProperties)
	s.MinItems = schemaInt64Ptr(withConstraints.MinItems)
	s.MinLength = schemaInt64Ptr(withConstraints.MinLength)
	s.MinProperties = schemaInt64Ptr(withConstraints.MinProperties)

	return nil
}

func (p *PrebuiltVoiceConfig) UnmarshalJSON(data []byte) error {
	type Alias struct {
		VoiceNameCamel string `json:"voiceName,omitempty"`
		VoiceNameSnake string `json:"voice_name,omitempty"`
	}
	var aux Alias
	if err := sonic.Unmarshal(data, &aux); err != nil {
		return err
	}
	p.VoiceName = aux.VoiceNameCamel
	if !hasJSONKey(data, "voiceName") {
		p.VoiceName = aux.VoiceNameSnake
	}
	return nil
}

func (p PrebuiltVoiceConfig) MarshalJSON() ([]byte, error) {
	type Alias struct {
		VoiceName string `json:"voiceName,omitempty"`
	}
	return providerUtils.MarshalSorted(Alias(p))
}

func (v *VoiceConfig) UnmarshalJSON(data []byte) error {
	type Alias struct {
		PrebuiltVoiceConfigCamel *PrebuiltVoiceConfig `json:"prebuiltVoiceConfig,omitempty"`
		PrebuiltVoiceConfigSnake *PrebuiltVoiceConfig `json:"prebuilt_voice_config,omitempty"`
	}
	var aux Alias
	if err := sonic.Unmarshal(data, &aux); err != nil {
		return err
	}
	v.PrebuiltVoiceConfig = aux.PrebuiltVoiceConfigCamel
	if !hasJSONKey(data, "prebuiltVoiceConfig") {
		v.PrebuiltVoiceConfig = aux.PrebuiltVoiceConfigSnake
	}
	return nil
}

func (v VoiceConfig) MarshalJSON() ([]byte, error) {
	type Alias struct {
		PrebuiltVoiceConfig *PrebuiltVoiceConfig `json:"prebuiltVoiceConfig,omitempty"`
	}
	return providerUtils.MarshalSorted(Alias(v))
}

func (s *SpeechConfig) UnmarshalJSON(data []byte) error {
	type Alias struct {
		VoiceConfigCamel             *VoiceConfig             `json:"voiceConfig,omitempty"`
		VoiceConfigSnake             *VoiceConfig             `json:"voice_config,omitempty"`
		MultiSpeakerVoiceConfigCamel *MultiSpeakerVoiceConfig `json:"multiSpeakerVoiceConfig,omitempty"`
		MultiSpeakerVoiceConfigSnake *MultiSpeakerVoiceConfig `json:"multi_speaker_voice_config,omitempty"`
		LanguageCodeCamel            string                   `json:"languageCode,omitempty"`
		LanguageCodeSnake            string                   `json:"language_code,omitempty"`
	}

	var aux Alias
	if err := sonic.Unmarshal(data, &aux); err != nil {
		return err
	}
	s.VoiceConfig = aux.VoiceConfigCamel
	if !hasJSONKey(data, "voiceConfig") {
		s.VoiceConfig = aux.VoiceConfigSnake
	}
	s.MultiSpeakerVoiceConfig = aux.MultiSpeakerVoiceConfigCamel
	if !hasJSONKey(data, "multiSpeakerVoiceConfig") {
		s.MultiSpeakerVoiceConfig = aux.MultiSpeakerVoiceConfigSnake
	}
	s.LanguageCode = aux.LanguageCodeCamel
	if !hasJSONKey(data, "languageCode") {
		s.LanguageCode = aux.LanguageCodeSnake
	}
	return nil
}

func (tc *GenerationConfigThinkingConfig) UnmarshalJSON(data []byte) error {

	type Alias struct {
		IncludeThoughts      *bool   `json:"includeThoughts"`
		IncludeThoughtsSnake *bool   `json:"include_thoughts"`
		ThinkingBudget       *int32  `json:"thinkingBudget"`
		ThinkingBudgetSnake  *int32  `json:"thinking_budget"`
		ThinkingLevel        *string `json:"thinkingLevel"`
		ThinkingLevelSnake   *string `json:"thinking_level"`
	}

	var aux Alias
	if err := sonic.Unmarshal(data, &aux); err != nil {
		return err
	}

	if aux.IncludeThoughts != nil {
		tc.IncludeThoughts = *aux.IncludeThoughts
	} else if aux.IncludeThoughtsSnake != nil {
		tc.IncludeThoughts = *aux.IncludeThoughtsSnake
	}

	if aux.ThinkingBudget != nil {
		tc.ThinkingBudget = aux.ThinkingBudget
	} else if aux.ThinkingBudgetSnake != nil {
		tc.ThinkingBudget = aux.ThinkingBudgetSnake
	}

	if aux.ThinkingLevel != nil {
		tc.ThinkingLevel = aux.ThinkingLevel
	} else if aux.ThinkingLevelSnake != nil {
		tc.ThinkingLevel = aux.ThinkingLevelSnake
	}

	return nil
}

func (r *GeminiBatchEmbeddingRequest) GetExtraParams() map[string]interface{} {
	return r.ExtraParams
}

func (r *GeminiEmbeddingRequest) GetExtraParams() map[string]interface{} {
	return r.ExtraParams
}

func (t *ToolCall) UnmarshalJSON(data []byte) error {
	type Alias ToolCall
	aux := struct {
		*Alias
		ToolTypeSnake string `json:"tool_type,omitempty"`
	}{Alias: (*Alias)(t)}
	if err := sonic.Unmarshal(data, &aux); err != nil {
		return err
	}
	if t.ToolType == "" && aux.ToolTypeSnake != "" {
		t.ToolType = aux.ToolTypeSnake
	}
	return nil
}

func (t *ToolResponse) UnmarshalJSON(data []byte) error {
	type Alias ToolResponse
	aux := struct {
		*Alias
		ToolTypeSnake string `json:"tool_type,omitempty"`
	}{Alias: (*Alias)(t)}
	if err := sonic.Unmarshal(data, &aux); err != nil {
		return err
	}
	if t.ToolType == "" && aux.ToolTypeSnake != "" {
		t.ToolType = aux.ToolTypeSnake
	}
	return nil
}

func isSearchToolType(toolType string) bool {
	return geminiServerSideSearchToolTypes[strings.ToUpper(toolType)]
}

func (p Part) MarshalJSON() ([]byte, error) {
	type PartAlias struct {
		VideoMetadata       *VideoMetadata       `json:"videoMetadata,omitempty"`
		MediaResolution     *PartMediaResolution `json:"mediaResolution,omitempty"`
		Thought             bool                 `json:"thought,omitempty"`
		InlineData          *Blob                `json:"inlineData,omitempty"`
		FileData            *FileData            `json:"fileData,omitempty"`
		ThoughtSignature    string               `json:"thoughtSignature,omitempty"`
		CodeExecutionResult *CodeExecutionResult `json:"codeExecutionResult,omitempty"`
		ExecutableCode      *ExecutableCode      `json:"executableCode,omitempty"`
		FunctionCall        *FunctionCall        `json:"functionCall,omitempty"`
		FunctionResponse    *FunctionResponse    `json:"functionResponse,omitempty"`
		ToolCall            *ToolCall            `json:"toolCall,omitempty"`
		ToolResponse        *ToolResponse        `json:"toolResponse,omitempty"`

		Text *string `json:"text,omitempty"`
	}

	aux := PartAlias{
		VideoMetadata:       p.VideoMetadata,
		MediaResolution:     p.MediaResolution,
		Thought:             p.Thought,
		InlineData:          p.InlineData,
		FileData:            p.FileData,
		CodeExecutionResult: p.CodeExecutionResult,
		ExecutableCode:      p.ExecutableCode,
		FunctionCall:        p.FunctionCall,
		FunctionResponse:    p.FunctionResponse,
		ToolCall:            p.ToolCall,
		ToolResponse:        p.ToolResponse,
	}
	if p.Text != "" {
		aux.Text = &p.Text
	}

	if len(p.ThoughtSignature) > 0 {
		if string(p.ThoughtSignature) == skipThoughtSignatureValidator {
			aux.ThoughtSignature = skipThoughtSignatureValidator
		} else {
			aux.ThoughtSignature = base64.StdEncoding.EncodeToString(p.ThoughtSignature)
		}
	}

	if aux.Text == nil && aux.ThoughtSignature != "" && !p.hasNonTextData() {
		aux.Text = new("")
	}

	return providerUtils.MarshalSorted(aux)
}

func (p Part) hasNonTextData() bool {
	return p.InlineData != nil ||
		p.FileData != nil ||
		p.CodeExecutionResult != nil ||
		p.ExecutableCode != nil ||
		p.FunctionCall != nil ||
		p.FunctionResponse != nil ||
		p.ToolCall != nil ||
		p.ToolResponse != nil
}

func (p *Part) UnmarshalJSON(data []byte) error {
	type PartAlias struct {
		VideoMetadata       *VideoMetadata       `json:"videoMetadata,omitempty"`
		MediaResolution     *PartMediaResolution `json:"mediaResolution,omitempty"`
		Thought             bool                 `json:"thought,omitempty"`
		InlineData          *Blob                `json:"inlineData,omitempty"`
		FileData            *FileData            `json:"fileData,omitempty"`
		ThoughtSignature    string               `json:"thoughtSignature,omitempty"`
		CodeExecutionResult *CodeExecutionResult `json:"codeExecutionResult,omitempty"`
		ExecutableCode      *ExecutableCode      `json:"executableCode,omitempty"`
		FunctionCall        *FunctionCall        `json:"functionCall,omitempty"`
		FunctionResponse    *FunctionResponse    `json:"functionResponse,omitempty"`
		ToolCall            *ToolCall            `json:"toolCall,omitempty"`
		ToolResponse        *ToolResponse        `json:"toolResponse,omitempty"`
		Text                string               `json:"text,omitempty"`

		InlineDataSnake      *Blob                `json:"inline_data,omitempty"`
		FileDataSnake        *FileData            `json:"file_data,omitempty"`
		MediaResolutionSnake *PartMediaResolution `json:"media_resolution,omitempty"`
	}

	var aux PartAlias
	if err := sonic.Unmarshal(data, &aux); err != nil {
		return err
	}

	p.VideoMetadata = aux.VideoMetadata
	p.Thought = aux.Thought
	p.InlineData = aux.InlineData
	if p.InlineData == nil {
		p.InlineData = aux.InlineDataSnake
	}
	p.FileData = aux.FileData
	if p.FileData == nil {
		p.FileData = aux.FileDataSnake
	}

	p.MediaResolution = aux.MediaResolution
	if !hasJSONKey(data, "mediaResolution") && aux.MediaResolutionSnake != nil {
		p.MediaResolution = aux.MediaResolutionSnake
	}
	p.CodeExecutionResult = aux.CodeExecutionResult
	p.ExecutableCode = aux.ExecutableCode
	p.ToolCall = aux.ToolCall
	p.ToolResponse = aux.ToolResponse
	p.FunctionCall = aux.FunctionCall
	p.FunctionResponse = aux.FunctionResponse
	p.Text = aux.Text

	if aux.ThoughtSignature != "" {
		if aux.ThoughtSignature == skipThoughtSignatureValidator {
			p.ThoughtSignature = []byte(skipThoughtSignatureValidator)
		} else {

			standardBase64 := strings.ReplaceAll(strings.ReplaceAll(aux.ThoughtSignature, "_", "/"), "-", "+")

			switch len(standardBase64) % 4 {
			case 2:
				standardBase64 += "=="
			case 3:
				standardBase64 += "="
			}
			decoded, err := base64.StdEncoding.DecodeString(standardBase64)
			if err != nil {
				return fmt.Errorf("failed to decode base64 thoughtSignature: %v", err)
			}
			p.ThoughtSignature = decoded
		}
	}

	return nil
}

func (m *PartMediaResolution) UnmarshalJSON(data []byte) error {
	type PartMediaResolutionAlias struct {
		Level          string `json:"level,omitempty"`
		NumTokens      *int32 `json:"numTokens,omitempty"`
		NumTokensSnake *int32 `json:"num_tokens,omitempty"`
	}

	var aux PartMediaResolutionAlias
	if err := sonic.Unmarshal(data, &aux); err != nil {
		return err
	}

	m.Level = aux.Level
	m.NumTokens = aux.NumTokens
	if !hasJSONKey(data, "numTokens") && aux.NumTokensSnake != nil {
		m.NumTokens = aux.NumTokensSnake
	}

	return nil
}

func (b *Blob) UnmarshalJSON(data []byte) error {
	type BlobAlias struct {
		DisplayName      string `json:"displayName,omitempty"`
		DisplayNameSnake string `json:"display_name,omitempty"`
		Data             string `json:"data,omitempty"`
		MIMEType         string `json:"mimeType,omitempty"`
		MIMETypeSnake    string `json:"mime_type,omitempty"`
	}

	var aux BlobAlias
	if err := sonic.Unmarshal(data, &aux); err != nil {
		return err
	}

	b.DisplayName = aux.DisplayName
	if b.DisplayName == "" {
		b.DisplayName = aux.DisplayNameSnake
	}
	b.MIMEType = aux.MIMEType
	if b.MIMEType == "" {
		b.MIMEType = aux.MIMETypeSnake
	}

	if aux.Data != "" {

		standardBase64 := strings.ReplaceAll(strings.ReplaceAll(aux.Data, "_", "/"), "-", "+")

		switch len(standardBase64) % 4 {
		case 2:
			standardBase64 += "=="
		case 3:
			standardBase64 += "="
		}
		b.Data = standardBase64
	} else {
		b.Data = aux.Data
	}

	return nil
}

const (
	OutcomeUnspecified Outcome = "OUTCOME_UNSPECIFIED"

	OutcomeOK Outcome = "OUTCOME_OK"

	OutcomeFailed Outcome = "OUTCOME_FAILED"

	OutcomeDeadlineExceeded Outcome = "OUTCOME_DEADLINE_EXCEEDED"
)

func (f *FileData) UnmarshalJSON(data []byte) error {
	type FileDataAlias struct {
		DisplayName      string `json:"displayName,omitempty"`
		DisplayNameSnake string `json:"display_name,omitempty"`
		FileURI          string `json:"fileUri,omitempty"`
		FileURISnake     string `json:"file_uri,omitempty"`
		MIMEType         string `json:"mimeType,omitempty"`
		MIMETypeSnake    string `json:"mime_type,omitempty"`
	}

	var aux FileDataAlias
	if err := sonic.Unmarshal(data, &aux); err != nil {
		return err
	}

	f.DisplayName = aux.DisplayName
	if f.DisplayName == "" {
		f.DisplayName = aux.DisplayNameSnake
	}
	f.FileURI = aux.FileURI
	if f.FileURI == "" {
		f.FileURI = aux.FileURISnake
	}
	f.MIMEType = aux.MIMEType
	if f.MIMEType == "" {
		f.MIMEType = aux.MIMETypeSnake
	}

	return nil
}

func (d *dateJSON) UnmarshalJSON(data []byte) error {
	m := make(map[string]int)
	if err := sonic.Unmarshal(data, &m); err != nil {
		return fmt.Errorf("failed to unmarshal date from map: %w", err)
	}

	if len(m) == 0 {
		return nil
	}
	if _, ok := m["year"]; !ok {
		return fmt.Errorf("key %q not found", "year")
	}
	d.Year = m["year"]

	if month, ok := m["month"]; ok {
		d.Month = time.Month(month)
	}
	if day, ok := m["day"]; ok {
		d.Day = day
	}
	return nil
}

func (d *dateJSON) MarshalJSON() ([]byte, error) {
	m := make(map[string]int)
	if d == nil || (civil.Date)(*d).IsZero() {
		return providerUtils.MarshalSorted(nil)
	}
	if d.Year != 0 {
		m["year"] = d.Year
	}
	if d.Month != 0 {
		m["month"] = int(d.Month)
	}
	if d.Day != 0 {
		m["day"] = d.Day
	}
	return providerUtils.MarshalSorted(m)
}

func (c *Citation) UnmarshalJSON(data []byte) error {
	type Alias Citation
	aux := &struct {
		PublicationDate *dateJSON `json:"publicationDate,omitempty"`
		*Alias
	}{
		Alias: (*Alias)(c),
	}

	if err := sonic.Unmarshal(data, &aux); err != nil {
		return err
	}

	if !reflect.ValueOf(aux.PublicationDate).IsZero() {
		c.PublicationDate = civil.Date(*aux.PublicationDate)
	}

	return nil
}

func (c *Citation) MarshalJSON() ([]byte, error) {
	type Alias Citation
	aux := &struct {
		PublicationDate *dateJSON `json:"publicationDate,omitempty"`
		*Alias
	}{
		Alias: (*Alias)(c),
	}

	if !reflect.ValueOf(c.PublicationDate).IsZero() {
		aux.PublicationDate = (*dateJSON)(&c.PublicationDate)
	}

	return providerUtils.MarshalSorted(aux)
}

func (g *GenerateContentResponse) UnmarshalJSON(data []byte) error {
	type Alias GenerateContentResponse
	aux := &struct {
		CreateTime *time.Time `json:"createTime,omitempty"`
		*Alias
	}{
		Alias: (*Alias)(g),
	}

	if err := sonic.Unmarshal(data, &aux); err != nil {
		return err
	}

	if !reflect.ValueOf(aux.CreateTime).IsZero() {
		g.CreateTime = time.Time(*aux.CreateTime)
	}

	return nil
}

func (g *GenerateContentResponse) MarshalJSON() ([]byte, error) {
	type Alias GenerateContentResponse
	aux := &struct {
		CreateTime *time.Time `json:"createTime,omitempty"`
		*Alias
	}{
		Alias: (*Alias)(g),
	}

	if !reflect.ValueOf(g.CreateTime).IsZero() {
		aux.CreateTime = (*time.Time)(&g.CreateTime)
	}

	return providerUtils.MarshalSorted(aux)
}

func (g *GeminiBatchStats) MarshalJSON() ([]byte, error) {
	return providerUtils.MarshalSorted(struct {
		RequestCount           string `json:"requestCount"`
		PendingRequestCount    string `json:"pendingRequestCount"`
		SuccessfulRequestCount string `json:"successfulRequestCount"`
	}{
		RequestCount:           strconv.Itoa(g.RequestCount),
		PendingRequestCount:    strconv.Itoa(g.PendingRequestCount),
		SuccessfulRequestCount: strconv.Itoa(g.SuccessfulRequestCount),
	})
}

func (g *GeminiBatchStats) UnmarshalJSON(data []byte) error {
	var raw struct {
		RequestCount           string `json:"requestCount"`
		PendingRequestCount    string `json:"pendingRequestCount"`
		SuccessfulRequestCount string `json:"successfulRequestCount"`
	}
	if err := sonic.Unmarshal(data, &raw); err != nil {
		return err
	}
	if raw.RequestCount != "" {
		val, err := strconv.Atoi(raw.RequestCount)
		if err != nil {
			return err
		}
		g.RequestCount = val
	}
	if raw.PendingRequestCount != "" {
		val, err := strconv.Atoi(raw.PendingRequestCount)
		if err != nil {
			return err
		}
		g.PendingRequestCount = val
	}
	if raw.SuccessfulRequestCount != "" {
		val, err := strconv.Atoi(raw.SuccessfulRequestCount)
		if err != nil {
			return err
		}
		g.SuccessfulRequestCount = val
	}
	return nil
}

const (
	GeminiBatchStateUnspecified = "BATCH_STATE_UNSPECIFIED"
	GeminiBatchStatePending     = "BATCH_STATE_PENDING"
	GeminiBatchStateRunning     = "BATCH_STATE_RUNNING"
	GeminiBatchStateSucceeded   = "BATCH_STATE_SUCCEEDED"
	GeminiBatchStateFailed      = "BATCH_STATE_FAILED"
	GeminiBatchStateCancelling  = "BATCH_STATE_CANCELLING"
	GeminiBatchStateCancelled   = "BATCH_STATE_CANCELLED"
	GeminiBatchStateExpired     = "BATCH_STATE_EXPIRED"
)

func (g *GeminiCountTokensRequest) UnmarshalJSON(data []byte) error {

	var envelope struct {
		GenerateContentRequest      *GeminiGenerationRequest `json:"generateContentRequest,omitempty"`
		GenerateContentRequestSnake *GeminiGenerationRequest `json:"generate_content_request,omitempty"`
	}
	if err := sonic.Unmarshal(data, &envelope); err != nil {
		return err
	}
	g.GenerateContentRequest = envelope.GenerateContentRequest

	if !hasJSONKey(data, "generateContentRequest") && envelope.GenerateContentRequestSnake != nil {
		g.GenerateContentRequest = envelope.GenerateContentRequestSnake
	}

	return g.GeminiGenerationRequest.UnmarshalJSON(data)
}

func (r *GeminiImagenRequest) GetExtraParams() map[string]interface{} {
	return r.ExtraParams
}

func (r *GeminiVideoGenerationRequest) GetExtraParams() map[string]interface{} {
	return r.ExtraParams
}

func (v *VideoImageData) UnmarshalJSON(data []byte) error {
	type VideoImageDataAlias struct {
		BytesBase64Encoded *string   `json:"bytesBase64Encoded,omitempty"`
		GCSURI             *string   `json:"gcsUri,omitempty"`
		FileData           *FileData `json:"fileData,omitempty"`
		MimeType           *string   `json:"mimeType,omitempty"`
		MaskMode           *string   `json:"maskMode,omitempty"`
	}

	var aux VideoImageDataAlias
	if err := sonic.Unmarshal(data, &aux); err != nil {
		return err
	}

	v.GCSURI = aux.GCSURI
	v.FileData = aux.FileData
	v.MimeType = aux.MimeType
	v.MaskMode = aux.MaskMode

	if aux.BytesBase64Encoded != nil && *aux.BytesBase64Encoded != "" {

		standardBase64 := *aux.BytesBase64Encoded

		standardBase64 = strings.ReplaceAll(standardBase64, "_", "/")
		standardBase64 = strings.ReplaceAll(standardBase64, "-", "+")

		switch len(standardBase64) % 4 {
		case 2:
			standardBase64 += "=="
		case 3:
			standardBase64 += "="
		}

		decoded, err := base64.StdEncoding.DecodeString(standardBase64)
		if err != nil {
			return fmt.Errorf("failed to decode base64 bytesBase64Encoded: %v", err)
		}

		normalized := base64.StdEncoding.EncodeToString(decoded)
		v.BytesBase64Encoded = &normalized
	} else {
		v.BytesBase64Encoded = aux.BytesBase64Encoded
	}

	return nil
}

func (f *GeminiFileUploadHandlerReqFile) UnmarshalJSON(data []byte) error {
	type Alias struct {
		DisplayNameCamel string `json:"displayName"`
		DisplayNameSnake string `json:"display_name"`
	}
	var alias Alias
	if err := sonic.Unmarshal(data, &alias); err != nil {
		return err
	}

	if alias.DisplayNameCamel != "" {
		f.DisplayName = alias.DisplayNameCamel
	} else {
		f.DisplayName = alias.DisplayNameSnake
	}
	return nil
}
