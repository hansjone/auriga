// Auriga modifications: trimmed relay core/providers; see third_party/bifrost/UPSTREAM.md.

package schemas

import (
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"net/url"
	"regexp"

	"strconv"
	"strings"
	"sync"
)

var grokModelsWithoutReasoningEffort = map[string]struct{}{
	"grok-3":                  {},
	"grok-4":                  {},
	"grok-4-fast-reasoning":   {},
	"grok-4-1-fast-reasoning": {},
	"grok-code-fast-1":        {},
}
var openAIDateRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

type ImageContentType string

var fileExtensionToMediaType = map[string]string{
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".png":  "image/png",
	".gif":  "image/gif",
	".webp": "image/webp",
	".svg":  "image/svg+xml",
	".bmp":  "image/bmp",
}
var knownProviders = func() map[string]bool {
	m := make(map[string]bool, len(StandardProviders))
	for _, p := range StandardProviders {
		m[string(p)] = true
	}
	return m
}()
var grokReasoningModels = []string{
	"grok-3",
	"grok-3-mini",
	"grok-4",
	"grok-4-fast-reasoning",
	"grok-4-1-fast-reasoning",
	"grok-code-fast-1",
}
var taggedVersionRe = regexp.MustCompile(
	`^(?:v\d+(?:\.\d+){0,2}|rc\d+|alpha|beta|preview|canary|experimental)$`,
)
var anthropicDateRe = regexp.MustCompile(`^\d{8}$`)

type URLTypeInfo struct {
	Type                 ImageContentType
	MediaType            *string
	DataURLWithoutPrefix *string
}

var defaultImageURLSchemes = []string{"http", "https"}
var knownProvidersMu sync.RWMutex
var base64Regex = regexp.MustCompile(`^[A-Za-z0-9+/]*={0,2}$`)
var grokModelsWithXHighReasoningEffort = map[string]struct{}{
	"grok-4.6":              {},
	"grok-4.7":              {},
	"grok-4.20-multi-agent": {},
}
var openAIDecisionModelPrefixes = []string{"gpt-6-luna"}
var dataURIRegex = regexp.MustCompile(`^data:([^,]*),([\s\S]+)$`)
var grokDateSuffixRe = regexp.MustCompile(`-\d{4}$`)

func Ptr[T any](v T) *T {
	return &v
}

const letters = "abcdef0123456789"

func GetRandomString(length int) string {
	if length <= 0 {
		return ""
	}
	b := make([]byte, length)
	for i := range b {
		b[i] = letters[rand.IntN(len(letters))]
	}
	return string(b)
}

func SecretVarAsString(e *SecretVar) string {
	if e == nil {
		return ""
	}
	if e.IsFromSecret() {
		return e.ref
	}
	return e.GetValue()
}

func RegisterKnownProvider(provider ModelProvider) {
	knownProvidersMu.Lock()
	defer knownProvidersMu.Unlock()
	knownProviders[string(provider)] = true
}

func IsKnownProvider(provider string) bool {
	knownProvidersMu.RLock()
	defer knownProvidersMu.RUnlock()
	return knownProviders[provider]
}

func ParseModelString(model string, defaultProvider ModelProvider) (ModelProvider, string) {

	if strings.Contains(model, "/") {
		parts := strings.SplitN(model, "/", 2)
		if len(parts) == 2 && IsKnownProvider(parts[0]) {
			return ModelProvider(parts[0]), parts[1]
		}
	}

	return defaultProvider, model
}

func IsAllDigitsASCII(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

func ParseFallbacks(fallbacks []string) []Fallback {
	if len(fallbacks) == 0 {
		return nil
	}
	parsedFallbacks := make([]Fallback, 0, len(fallbacks))
	for _, fallback := range fallbacks {
		if fallback == "" {
			continue
		}
		fallbackProvider, fallbackModel := ParseModelString(fallback, "")
		if fallbackProvider != "" && fallbackModel != "" {
			parsedFallbacks = append(parsedFallbacks, Fallback{Provider: fallbackProvider, Model: fallbackModel})
		}
	}
	return parsedFallbacks
}

const (
	ImageContentTypeBase64 ImageContentType = "base64"
	ImageContentTypeURL    ImageContentType = "url"
)

func SanitizeImageURL(rawURL string) (string, error) {
	return sanitizeImageURL(rawURL, defaultImageURLSchemes)
}

func SanitizeImageURLWithAllowedSchemes(rawURL string, allowedSchemes ...string) (string, error) {
	return sanitizeImageURL(rawURL, allowedSchemes)
}

func sanitizeImageURL(rawURL string, allowedSchemes []string) (string, error) {
	if rawURL == "" {
		return rawURL, fmt.Errorf("URL cannot be empty")
	}

	rawURL = strings.TrimSpace(rawURL)

	if strings.HasPrefix(rawURL, "data:") {

		if _, _, _, ok := ParseDataURL(rawURL); !ok {
			return rawURL, fmt.Errorf("invalid data URL format")
		}
		return rawURL, nil
	}

	if isLikelyBase64(rawURL) {

		mediaType := detectImageTypeFromBase64(rawURL)

		cleanBase64 := strings.ReplaceAll(strings.ReplaceAll(rawURL, "\n", ""), " ", "")

		return fmt.Sprintf("data:%s;base64,%s", mediaType, cleanBase64), nil
	}

	parsedURL, err := url.Parse(rawURL)
	if err != nil {
		return rawURL, fmt.Errorf("invalid URL format: %w", err)
	}

	if parsedURL.Scheme == "" {
		return rawURL, fmt.Errorf("URL must have a valid scheme")
	}

	if len(allowedSchemes) == 0 {
		return rawURL, fmt.Errorf("URL scheme %q is not allowed: no schemes permitted", parsedURL.Scheme)
	}
	allowed := false
	for _, scheme := range allowedSchemes {
		if strings.EqualFold(parsedURL.Scheme, scheme) {
			allowed = true
			break
		}
	}
	if !allowed {
		return rawURL, fmt.Errorf("URL scheme %q is not allowed; expected one of: %s", parsedURL.Scheme, strings.Join(allowedSchemes, ", "))
	}

	if parsedURL.Host == "" {
		return rawURL, fmt.Errorf("URL must have a valid host")
	}

	return parsedURL.String(), nil
}

func ExtractURLTypeInfo(sanitizedURL string) URLTypeInfo {
	if strings.HasPrefix(sanitizedURL, "data:") {
		return extractDataURLInfo(sanitizedURL)
	}
	return extractRegularURLInfo(sanitizedURL)
}

func ParseDataURL(dataURL string) (mediaType string, isBase64 bool, payload string, ok bool) {
	matches := dataURIRegex.FindStringSubmatch(dataURL)
	if len(matches) != 3 {
		return "", false, "", false
	}

	segments := strings.Split(matches[1], ";")
	mediaType = strings.ToLower(strings.TrimSpace(segments[0]))
	if mediaType == "" {
		return "", false, "", false
	}
	for _, segment := range segments[1:] {
		if strings.EqualFold(strings.TrimSpace(segment), "base64") {
			isBase64 = true
		}
	}

	return mediaType, isBase64, matches[2], true
}

func extractDataURLInfo(dataURL string) URLTypeInfo {
	mediaType, isBase64, payload, ok := ParseDataURL(dataURL)
	if !ok {
		return URLTypeInfo{Type: ImageContentTypeBase64}
	}

	dataURLWithoutPrefix := dataURL
	if isBase64 {
		dataURLWithoutPrefix = payload
	}

	info := URLTypeInfo{
		MediaType:            &mediaType,
		DataURLWithoutPrefix: &dataURLWithoutPrefix,
	}

	if isBase64 {
		info.Type = ImageContentTypeBase64
	} else {
		info.Type = ImageContentTypeURL
	}

	return info
}

func extractRegularURLInfo(regularURL string) URLTypeInfo {
	info := URLTypeInfo{
		Type: ImageContentTypeURL,
	}

	parsedURL, err := url.Parse(regularURL)
	if err != nil {
		return info
	}

	path := strings.ToLower(parsedURL.Path)

	for ext, mediaType := range fileExtensionToMediaType {
		if strings.HasSuffix(path, ext) {
			info.MediaType = &mediaType
			break
		}
	}

	return info
}

func detectImageTypeFromBase64(base64Data string) string {

	cleanData := strings.ReplaceAll(strings.ReplaceAll(base64Data, "\n", ""), " ", "")

	switch {
	case strings.HasPrefix(cleanData, "/9j/") || strings.HasPrefix(cleanData, "/9k/"):

		return "image/jpeg"
	case strings.HasPrefix(cleanData, "iVBORw0KGgo"):

		return "image/png"
	case strings.HasPrefix(cleanData, "R0lGOD"):

		return "image/gif"
	case strings.HasPrefix(cleanData, "Qk"):

		return "image/bmp"
	case strings.HasPrefix(cleanData, "UklGR") && len(cleanData) >= 16 && cleanData[12:16] == "V0VC":

		return "image/webp"
	case strings.HasPrefix(cleanData, "PHN2Zy") || strings.HasPrefix(cleanData, "PD94bW"):

		return "image/svg+xml"
	default:

		return "image/jpeg"
	}
}

func isLikelyBase64(s string) bool {

	cleanData := strings.ReplaceAll(strings.ReplaceAll(s, "\n", ""), " ", "")

	return base64Regex.MatchString(cleanData)
}

func SafeExtractString(value interface{}) (string, bool) {
	if value == nil {
		return "", false
	}
	switch v := value.(type) {
	case string:
		return v, true
	case *string:
		if v != nil {
			return *v, true
		}
		return "", false
	case json.Number:
		return string(v), true
	default:
		return "", false
	}
}

func SafeExtractInt(value interface{}) (int, bool) {
	if value == nil {
		return 0, false
	}
	switch v := value.(type) {
	case int:
		return v, true
	case int8:
		return int(v), true
	case int16:
		return int(v), true
	case int32:
		return int(v), true
	case int64:
		return int(v), true
	case uint:
		return int(v), true
	case uint8:
		return int(v), true
	case uint16:
		return int(v), true
	case uint32:
		return int(v), true
	case uint64:
		return int(v), true
	case float32:
		return int(v), true
	case float64:
		return int(v), true
	case json.Number:
		if intVal, err := v.Int64(); err == nil {
			return int(intVal), true
		}
		return 0, false
	case string:
		if intVal, err := strconv.Atoi(v); err == nil {
			return intVal, true
		}
		return 0, false
	default:
		return 0, false
	}
}

func SafeExtractFloat64(value interface{}) (float64, bool) {
	if value == nil {
		return 0, false
	}
	switch v := value.(type) {
	case float64:
		return v, true
	case float32:
		return float64(v), true
	case int:
		return float64(v), true
	case int8:
		return float64(v), true
	case int16:
		return float64(v), true
	case int32:
		return float64(v), true
	case int64:
		return float64(v), true
	case uint:
		return float64(v), true
	case uint8:
		return float64(v), true
	case uint16:
		return float64(v), true
	case uint32:
		return float64(v), true
	case uint64:
		return float64(v), true
	case json.Number:
		if floatVal, err := v.Float64(); err == nil {
			return floatVal, true
		}
		return 0, false
	case string:
		if floatVal, err := strconv.ParseFloat(v, 64); err == nil {
			return floatVal, true
		}
		return 0, false
	default:
		return 0, false
	}
}

func SafeExtractBool(value interface{}) (bool, bool) {
	if value == nil {
		return false, false
	}
	switch v := value.(type) {
	case bool:
		return v, true
	case *bool:
		if v != nil {
			return *v, true
		}
		return false, false
	case string:
		if boolVal, err := strconv.ParseBool(v); err == nil {
			return boolVal, true
		}
		return false, false
	case int:
		return v != 0, true
	case int8:
		return v != 0, true
	case int16:
		return v != 0, true
	case int32:
		return v != 0, true
	case int64:
		return v != 0, true
	case uint:
		return v != 0, true
	case uint8:
		return v != 0, true
	case uint16:
		return v != 0, true
	case uint32:
		return v != 0, true
	case uint64:
		return v != 0, true
	case float32:
		return v != 0, true
	case float64:
		return v != 0, true
	default:
		return false, false
	}
}

func SafeExtractStringSlice(value interface{}) ([]string, bool) {
	if value == nil {
		return nil, false
	}
	switch v := value.(type) {
	case []string:
		return v, true
	case []interface{}:
		var result []string
		for _, item := range v {
			if str, ok := SafeExtractString(item); ok {
				result = append(result, str)
			} else {
				return nil, false
			}
		}
		return result, true
	case []*string:
		var result []string
		for _, item := range v {
			if item != nil {
				result = append(result, *item)
			}
		}
		return result, true
	default:
		return nil, false
	}
}

func SafeExtractStringPointer(value interface{}) (*string, bool) {
	if value == nil {
		return nil, false
	}
	switch v := value.(type) {
	case *string:
		return v, true
	case string:
		return &v, true
	case json.Number:
		str := string(v)
		return &str, true
	default:
		return nil, false
	}
}

func SafeExtractIntPointer(value interface{}) (*int, bool) {
	if value == nil {
		return nil, false
	}
	if intVal, ok := SafeExtractInt(value); ok {
		return &intVal, true
	}
	return nil, false
}

func SafeExtractFloat64Pointer(value interface{}) (*float64, bool) {
	if value == nil {
		return nil, false
	}
	if floatVal, ok := SafeExtractFloat64(value); ok {
		return &floatVal, true
	}
	return nil, false
}

func SafeExtractBoolPointer(value interface{}) (*bool, bool) {
	if value == nil {
		return nil, false
	}
	if boolVal, ok := SafeExtractBool(value); ok {
		return &boolVal, true
	}
	return nil, false
}

func SafeExtractFromMap(m map[string]interface{}, key string) (interface{}, bool) {
	if m == nil {
		return nil, false
	}
	value, exists := m[key]
	return value, exists
}

func SafeExtractStringMap(value interface{}) (map[string]string, bool) {
	if value == nil {
		return nil, false
	}
	switch v := value.(type) {
	case map[string]string:
		return v, true
	case map[string]interface{}:
		result := make(map[string]string, len(v))
		for key, val := range v {
			if str, ok := SafeExtractString(val); ok {
				result[key] = str
			} else {
				return nil, false
			}
		}
		return result, true
	default:
		return nil, false
	}
}

func SafeExtractOrderedMap(value interface{}) (*OrderedMap, bool) {
	if value == nil {
		return nil, false
	}
	switch v := value.(type) {
	case map[string]interface{}:
		mapped := OrderedMapFromMap(v)
		if mapped != nil {
			return mapped, true
		}
		return nil, false
	case *map[string]interface{}:
		if v != nil {
			mapped := OrderedMapFromMap(*v)
			if mapped != nil {
				return mapped, true
			}
		}
		return nil, false
	case *OrderedMap:
		if v != nil {
			return v, true
		}
		return nil, false
	case OrderedMap:
		return &v, true
	case json.RawMessage:

		if strings.TrimSpace(string(v)) == "" || strings.TrimSpace(string(v))[0] != '{' {
			return nil, false
		}
		decoded := NewOrderedMap()
		if err := decoded.UnmarshalJSON(v); err != nil {
			return nil, false
		}
		return decoded, true
	}
	return nil, false
}

func DeepCopy(in interface{}) interface{} {
	var out interface{}
	b, err := json.Marshal(in)
	if err != nil {
		return in
	}
	if err = json.Unmarshal(b, &out); err != nil {
		return in
	}
	return out
}

func DeepCopyChatMessage(original ChatMessage) ChatMessage {
	copy := ChatMessage{}

	if original.Name != nil {
		copyName := *original.Name
		copy.Name = &copyName
	}

	copy.Role = original.Role

	if original.Content != nil {
		copy.Content = &ChatMessageContent{}

		if original.Content.ContentStr != nil {
			copyContentStr := *original.Content.ContentStr
			copy.Content.ContentStr = &copyContentStr
		}

		if original.Content.ContentBlocks != nil {
			copy.Content.ContentBlocks = make([]ChatContentBlock, len(original.Content.ContentBlocks))
			for i, block := range original.Content.ContentBlocks {
				copy.Content.ContentBlocks[i] = deepCopyChatContentBlock(block)
			}
		}
	}

	if original.ChatToolMessage != nil {
		copy.ChatToolMessage = &ChatToolMessage{}
		if original.ChatToolMessage.ToolCallID != nil {
			copyToolCallID := *original.ChatToolMessage.ToolCallID
			copy.ChatToolMessage.ToolCallID = &copyToolCallID
		}
	}

	if original.ChatAssistantMessage != nil {
		copy.ChatAssistantMessage = &ChatAssistantMessage{}

		if original.ChatAssistantMessage.Refusal != nil {
			copyRefusal := *original.ChatAssistantMessage.Refusal
			copy.ChatAssistantMessage.Refusal = &copyRefusal
		}

		if original.ChatAssistantMessage.Annotations != nil {
			copy.ChatAssistantMessage.Annotations = make([]ChatAssistantMessageAnnotation, len(original.ChatAssistantMessage.Annotations))
			for i, annotation := range original.ChatAssistantMessage.Annotations {
				copyAnnotation := ChatAssistantMessageAnnotation{
					Type: annotation.Type,
					URLCitation: ChatAssistantMessageAnnotationCitation{
						StartIndex: annotation.URLCitation.StartIndex,
						EndIndex:   annotation.URLCitation.EndIndex,
						Title:      annotation.URLCitation.Title,
					},
				}
				if annotation.URLCitation.URL != nil {
					copyURL := *annotation.URLCitation.URL
					copyAnnotation.URLCitation.URL = &copyURL
				}
				if annotation.URLCitation.Text != nil {
					copyText := *annotation.URLCitation.Text
					copyAnnotation.URLCitation.Text = &copyText
				}
				if annotation.URLCitation.Sources != nil {
					copySources := *annotation.URLCitation.Sources
					copyAnnotation.URLCitation.Sources = &copySources
				}
				if annotation.URLCitation.Type != nil {
					copyType := *annotation.URLCitation.Type
					copyAnnotation.URLCitation.Type = &copyType
				}
				copy.ChatAssistantMessage.Annotations[i] = copyAnnotation
			}
		}

		if original.ChatAssistantMessage.ToolCalls != nil {
			copy.ChatAssistantMessage.ToolCalls = make([]ChatAssistantMessageToolCall, len(original.ChatAssistantMessage.ToolCalls))
			for i, toolCall := range original.ChatAssistantMessage.ToolCalls {
				copyToolCall := ChatAssistantMessageToolCall{
					Index: toolCall.Index,
					Function: ChatAssistantMessageToolCallFunction{
						Arguments: toolCall.Function.Arguments,
					},
				}
				if toolCall.Type != nil {
					copyType := *toolCall.Type
					copyToolCall.Type = &copyType
				}
				if toolCall.ID != nil {
					copyID := *toolCall.ID
					copyToolCall.ID = &copyID
				}
				if toolCall.Function.Name != nil {
					copyName := *toolCall.Function.Name
					copyToolCall.Function.Name = &copyName
				}
				if len(toolCall.ExtraContent) > 0 {
					copyToolCall.ExtraContent = append(json.RawMessage(nil), toolCall.ExtraContent...)
				}
				copy.ChatAssistantMessage.ToolCalls[i] = copyToolCall
			}
		}
	}

	return copy
}

func deepCopyChatContentBlock(original ChatContentBlock) ChatContentBlock {
	copy := ChatContentBlock{
		Type: original.Type,
	}

	if original.Text != nil {
		copyText := *original.Text
		copy.Text = &copyText
	}

	if original.Refusal != nil {
		copyRefusal := *original.Refusal
		copy.Refusal = &copyRefusal
	}

	if original.ImageURLStruct != nil {
		copyImage := ChatInputImage{
			URL: original.ImageURLStruct.URL,
		}
		if original.ImageURLStruct.FileID != nil {
			copyFileID := *original.ImageURLStruct.FileID
			copyImage.FileID = &copyFileID
		}
		if original.ImageURLStruct.Detail != nil {
			copyDetail := *original.ImageURLStruct.Detail
			copyImage.Detail = &copyDetail
		}
		copy.ImageURLStruct = &copyImage
	}

	if original.InputAudio != nil {
		copyAudio := ChatInputAudio{
			Data: original.InputAudio.Data,
		}
		if original.InputAudio.Format != nil {
			copyFormat := *original.InputAudio.Format
			copyAudio.Format = &copyFormat
		}
		copy.InputAudio = &copyAudio
	}

	if original.File != nil {
		copyFile := ChatInputFile{}
		if original.File.FileData != nil {
			copyFileData := *original.File.FileData
			copyFile.FileData = &copyFileData
		}
		if original.File.FileID != nil {
			copyFileID := *original.File.FileID
			copyFile.FileID = &copyFileID
		}
		if original.File.Filename != nil {
			copyFilename := *original.File.Filename
			copyFile.Filename = &copyFilename
		}
		copy.File = &copyFile
	}

	if original.GuardContent != nil {
		copyGuardContent := &GuardContent{}
		if original.GuardContent.Qualifiers != nil {
			copyGuardContent.Qualifiers = append([]string(nil), original.GuardContent.Qualifiers...)
		}
		copy.GuardContent = copyGuardContent
	}

	if original.CacheControl != nil {
		copyCacheControl := &CacheControl{Type: original.CacheControl.Type}
		if original.CacheControl.TTL != nil {
			copyTTL := *original.CacheControl.TTL
			copyCacheControl.TTL = &copyTTL
		}
		if original.CacheControl.Scope != nil {
			copyScope := *original.CacheControl.Scope
			copyCacheControl.Scope = &copyScope
		}
		copy.CacheControl = copyCacheControl
	}

	if original.Citations != nil {
		copyCitations := &Citations{}
		if original.Citations.Enabled != nil {
			copyEnabled := *original.Citations.Enabled
			copyCitations.Enabled = &copyEnabled
		}
		copy.Citations = copyCitations
	}

	if original.PromptCacheBreakpoint != nil {
		copyPromptCacheBreakpoint := &PromptCacheBreakpoint{}
		if original.PromptCacheBreakpoint.Mode != nil {
			copyMode := *original.PromptCacheBreakpoint.Mode
			copyPromptCacheBreakpoint.Mode = &copyMode
		}
		copy.PromptCacheBreakpoint = copyPromptCacheBreakpoint
	}

	if original.CachePoint != nil {
		copyCachePoint := &CachePoint{Type: original.CachePoint.Type}
		if original.CachePoint.TTL != nil {
			copyTTL := *original.CachePoint.TTL
			copyCachePoint.TTL = &copyTTL
		}
		copy.CachePoint = copyCachePoint
	}

	return copy
}

func DeepCopyToolFunctionParameters(original *ToolFunctionParameters) *ToolFunctionParameters {
	if original == nil {
		return nil
	}

	copyParams := &ToolFunctionParameters{
		Type:                original.Type,
		keyOrder:            JSONKeyOrder{keys: append([]string(nil), original.keyOrder.keys...)},
		explicitEmptyObject: original.explicitEmptyObject,
	}

	if original.Description != nil {
		copyParamDesc := *original.Description
		copyParams.Description = &copyParamDesc
	}
	if original.Required != nil {
		copyParams.Required = append([]string(nil), original.Required...)
	}
	if original.Properties != nil {
		copyParams.Properties = deepCopyOrderedMap(original.Properties)
	}
	if original.AdditionalProperties != nil {
		copyAdditionalProps := AdditionalPropertiesStruct{}
		if original.AdditionalProperties.AdditionalPropertiesBool != nil {
			b := *original.AdditionalProperties.AdditionalPropertiesBool
			copyAdditionalProps.AdditionalPropertiesBool = &b
		}
		if original.AdditionalProperties.AdditionalPropertiesMap != nil {
			copyAdditionalProps.AdditionalPropertiesMap = deepCopyOrderedMap(original.AdditionalProperties.AdditionalPropertiesMap)
		}
		copyParams.AdditionalProperties = &copyAdditionalProps
	}
	if original.Enum != nil {
		copyParams.Enum = append([]string(nil), original.Enum...)
	}
	if original.Defs != nil {
		copyParams.Defs = deepCopyOrderedMap(original.Defs)
	}
	if original.Definitions != nil {
		copyParams.Definitions = deepCopyOrderedMap(original.Definitions)
	}
	if original.Ref != nil {
		ref := *original.Ref
		copyParams.Ref = &ref
	}
	if original.Items != nil {
		copyParams.Items = deepCopyOrderedMap(original.Items)
	}
	if original.MinItems != nil {
		minItems := *original.MinItems
		copyParams.MinItems = &minItems
	}
	if original.MaxItems != nil {
		maxItems := *original.MaxItems
		copyParams.MaxItems = &maxItems
	}
	copyParams.AnyOf = deepCopyOrderedMapSlice(original.AnyOf)
	copyParams.OneOf = deepCopyOrderedMapSlice(original.OneOf)
	copyParams.AllOf = deepCopyOrderedMapSlice(original.AllOf)
	if original.Format != nil {
		format := *original.Format
		copyParams.Format = &format
	}
	if original.Pattern != nil {
		pattern := *original.Pattern
		copyParams.Pattern = &pattern
	}
	if original.MinLength != nil {
		minLength := *original.MinLength
		copyParams.MinLength = &minLength
	}
	if original.MaxLength != nil {
		maxLength := *original.MaxLength
		copyParams.MaxLength = &maxLength
	}
	if original.Minimum != nil {
		minimum := *original.Minimum
		copyParams.Minimum = &minimum
	}
	if original.Maximum != nil {
		maximum := *original.Maximum
		copyParams.Maximum = &maximum
	}
	if original.Title != nil {
		title := *original.Title
		copyParams.Title = &title
	}
	copyParams.Default = DeepCopy(original.Default)
	if original.Nullable != nil {
		nullable := *original.Nullable
		copyParams.Nullable = &nullable
	}

	return copyParams
}

func deepCopyOrderedMap(original *OrderedMap) *OrderedMap {
	if original == nil {
		return nil
	}
	copyMap := NewOrderedMapWithCapacity(original.Len())
	original.Range(func(k string, v interface{}) bool {
		copyMap.Set(k, deepCopySchemaValue(v))
		return true
	})
	return copyMap
}

func deepCopyOrderedMapSlice(original []OrderedMap) []OrderedMap {
	if original == nil {
		return nil
	}
	copied := make([]OrderedMap, len(original))
	for i := range original {
		if copyMap := deepCopyOrderedMap(&original[i]); copyMap != nil {
			copied[i] = *copyMap
		}
	}
	return copied
}

func deepCopySchemaValue(original interface{}) interface{} {
	switch v := original.(type) {
	case *OrderedMap:
		return deepCopyOrderedMap(v)
	case OrderedMap:
		return deepCopyOrderedMap(&v)
	case map[string]interface{}:
		copied := make(map[string]interface{}, len(v))
		for key, value := range v {
			copied[key] = deepCopySchemaValue(value)
		}
		return copied
	case []interface{}:
		copied := make([]interface{}, len(v))
		for i, value := range v {
			copied[i] = deepCopySchemaValue(value)
		}
		return copied
	default:
		return DeepCopy(v)
	}
}

func deepCopyComputerToolCallAction(original ResponsesComputerToolCallAction) ResponsesComputerToolCallAction {
	copied := original
	copied.X = clonePtr(original.X)
	copied.Y = clonePtr(original.Y)
	copied.Button = clonePtr(original.Button)
	copied.ScrollX = clonePtr(original.ScrollX)
	copied.ScrollY = clonePtr(original.ScrollY)
	copied.Text = clonePtr(original.Text)
	if original.Path != nil {
		copied.Path = append([]ResponsesComputerToolCallActionPath(nil), original.Path...)
	}
	if original.Keys != nil {
		copied.Keys = append([]string(nil), original.Keys...)
	}
	if original.Region != nil {
		copied.Region = append([]int(nil), original.Region...)
	}
	return copied
}

func clonePtr[T any](p *T) *T {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}

func DeepCopyResponsesMessage(original ResponsesMessage) ResponsesMessage {
	copy := ResponsesMessage{}

	if original.ID != nil {
		copyID := *original.ID
		copy.ID = &copyID
	}

	if original.Type != nil {
		copyType := *original.Type
		copy.Type = &copyType
	}

	if original.Status != nil {
		copyStatus := *original.Status
		copy.Status = &copyStatus
	}

	if original.Phase != nil {
		copy.Phase = new(string)
		*copy.Phase = *original.Phase
	}

	if original.Role != nil {
		copyRole := *original.Role
		copy.Role = &copyRole
	}
	if original.CacheControl != nil {
		copyCacheControl := &CacheControl{Type: original.CacheControl.Type}
		if original.CacheControl.TTL != nil {
			copyTTL := *original.CacheControl.TTL
			copyCacheControl.TTL = &copyTTL
		}
		if original.CacheControl.Scope != nil {
			copyScope := *original.CacheControl.Scope
			copyCacheControl.Scope = &copyScope
		}
		copy.CacheControl = copyCacheControl
	}

	if original.Author != nil {
		copy.Author = append(json.RawMessage(nil), original.Author...)
	}
	if original.Recipient != nil {
		copy.Recipient = append(json.RawMessage(nil), original.Recipient...)
	}
	if original.ToolSearchOutputTools != nil {
		copy.ToolSearchOutputTools = append(json.RawMessage(nil), original.ToolSearchOutputTools...)
	}
	if original.AdditionalTools != nil {
		copy.AdditionalTools = append(json.RawMessage(nil), original.AdditionalTools...)
	}

	if original.ProviderNativeParts != nil {
		copy.ProviderNativeParts = append(json.RawMessage(nil), original.ProviderNativeParts...)
	}

	if original.rawPreserved != nil {
		copy.rawPreserved = append([]byte(nil), original.rawPreserved...)
	}

	if original.Content != nil {
		copy.Content = &ResponsesMessageContent{}

		if original.Content.ContentStr != nil {
			copyContentStr := *original.Content.ContentStr
			copy.Content.ContentStr = &copyContentStr
		}

		if original.Content.ContentBlocks != nil {
			copy.Content.ContentBlocks = make([]ResponsesMessageContentBlock, len(original.Content.ContentBlocks))
			for i, block := range original.Content.ContentBlocks {
				copy.Content.ContentBlocks[i] = deepCopyResponsesMessageContentBlock(block)
			}
		}
	}

	if original.ResponsesToolMessage != nil {
		copy.ResponsesToolMessage = &ResponsesToolMessage{}

		if original.ResponsesToolMessage.CallID != nil {
			copyCallID := *original.ResponsesToolMessage.CallID
			copy.ResponsesToolMessage.CallID = &copyCallID
		}

		if original.ResponsesToolMessage.Name != nil {
			copyName := *original.ResponsesToolMessage.Name
			copy.ResponsesToolMessage.Name = &copyName
		}

		if original.ResponsesToolMessage.Arguments != nil {
			copyArguments := *original.ResponsesToolMessage.Arguments
			copy.ResponsesToolMessage.Arguments = &copyArguments
		}

		if original.ResponsesToolMessage.ResponsesCustomToolCall != nil {
			copyCustomToolCall := *original.ResponsesToolMessage.ResponsesCustomToolCall
			copy.ResponsesToolMessage.ResponsesCustomToolCall = &copyCustomToolCall
		}

		if original.ResponsesToolMessage.Namespace != nil {
			copyNamespace := *original.ResponsesToolMessage.Namespace
			copy.ResponsesToolMessage.Namespace = &copyNamespace
		}

		if original.ResponsesToolMessage.Execution != nil {
			copyExecution := *original.ResponsesToolMessage.Execution
			copy.ResponsesToolMessage.Execution = &copyExecution
		}

		if original.ResponsesToolMessage.Async != nil {
			copy.ResponsesToolMessage.Async = new(*original.ResponsesToolMessage.Async)
		}

		if original.ResponsesToolMessage.Error != nil {
			copyError := ResponsesToolMessageError{}
			if original.ResponsesToolMessage.Error.ResponsesToolMessageErrorStr != nil {
				copyErrorStr := *original.ResponsesToolMessage.Error.ResponsesToolMessageErrorStr
				copyError.ResponsesToolMessageErrorStr = &copyErrorStr
			}
			if original.ResponsesToolMessage.Error.ResponsesToolMessageErrorStruct != nil {
				copyErrorStruct := *original.ResponsesToolMessage.Error.ResponsesToolMessageErrorStruct
				if copyErrorStruct.Code != nil {
					copyCode := *copyErrorStruct.Code
					copyErrorStruct.Code = &copyCode
				}
				if copyErrorStruct.Message != nil {
					copyMessage := *copyErrorStruct.Message
					copyErrorStruct.Message = &copyMessage
				}
				copyErrorStruct.Content = append(json.RawMessage(nil), copyErrorStruct.Content...)
				copyError.ResponsesToolMessageErrorStruct = &copyErrorStruct
			}
			copy.ResponsesToolMessage.Error = &copyError
		}

		if original.ResponsesToolMessage.Output != nil {
			copy.ResponsesToolMessage.Output = &ResponsesToolMessageOutputStruct{}

			if original.ResponsesToolMessage.Output.ResponsesToolCallOutputStr != nil {
				copyStr := *original.ResponsesToolMessage.Output.ResponsesToolCallOutputStr
				copy.ResponsesToolMessage.Output.ResponsesToolCallOutputStr = &copyStr
			}

			if original.ResponsesToolMessage.Output.ResponsesFunctionToolCallOutputBlocks != nil {
				copy.ResponsesToolMessage.Output.ResponsesFunctionToolCallOutputBlocks = make([]ResponsesMessageContentBlock, len(original.ResponsesToolMessage.Output.ResponsesFunctionToolCallOutputBlocks))
				for i, block := range original.ResponsesToolMessage.Output.ResponsesFunctionToolCallOutputBlocks {
					copy.ResponsesToolMessage.Output.ResponsesFunctionToolCallOutputBlocks[i] = deepCopyResponsesMessageContentBlock(block)
				}
			}

			if original.ResponsesToolMessage.Output.ResponsesShellCallOutput != nil {
				copy.ResponsesToolMessage.Output.ResponsesShellCallOutput = deepCopyShellCallOutput(original.ResponsesToolMessage.Output.ResponsesShellCallOutput)
			}

			if original.ResponsesToolMessage.Output.ResponsesComputerToolCallOutput != nil {
				copyOutput := *original.ResponsesToolMessage.Output.ResponsesComputerToolCallOutput
				copy.ResponsesToolMessage.Output.ResponsesComputerToolCallOutput = &copyOutput
			}
		}

		if original.ResponsesToolMessage.Action != nil {
			copy.ResponsesToolMessage.Action = &ResponsesToolMessageActionStruct{}

			if original.ResponsesToolMessage.Action.ResponsesToolCallActionStr != nil {
				copyActionString := *original.ResponsesToolMessage.Action.ResponsesToolCallActionStr
				copy.ResponsesToolMessage.Action.ResponsesToolCallActionStr = &copyActionString
			}

			if original.ResponsesToolMessage.Action.ResponsesComputerToolCallAction != nil {
				copyAction := deepCopyComputerToolCallAction(*original.ResponsesToolMessage.Action.ResponsesComputerToolCallAction)
				copy.ResponsesToolMessage.Action.ResponsesComputerToolCallAction = &copyAction
			}

			if original.ResponsesToolMessage.Action.ResponsesWebSearchToolCallAction != nil {
				copyAction := *original.ResponsesToolMessage.Action.ResponsesWebSearchToolCallAction
				copy.ResponsesToolMessage.Action.ResponsesWebSearchToolCallAction = &copyAction
			}

			if original.ResponsesToolMessage.Action.ResponsesWebFetchToolCallAction != nil {
				copyAction := *original.ResponsesToolMessage.Action.ResponsesWebFetchToolCallAction
				copy.ResponsesToolMessage.Action.ResponsesWebFetchToolCallAction = &copyAction
			}

			if original.ResponsesToolMessage.Action.ResponsesLocalShellToolCallAction != nil {
				copyAction := *original.ResponsesToolMessage.Action.ResponsesLocalShellToolCallAction
				copy.ResponsesToolMessage.Action.ResponsesLocalShellToolCallAction = &copyAction
			}

			if original.ResponsesToolMessage.Action.ResponsesShellToolCallAction != nil {
				copyAction := *original.ResponsesToolMessage.Action.ResponsesShellToolCallAction
				copyAction.Commands = append([]string(nil), copyAction.Commands...)
				if copyAction.TimeoutMS != nil {
					timeoutMS := *copyAction.TimeoutMS
					copyAction.TimeoutMS = &timeoutMS
				}
				if copyAction.MaxOutputLength != nil {
					maxOutputLength := *copyAction.MaxOutputLength
					copyAction.MaxOutputLength = &maxOutputLength
				}
				copy.ResponsesToolMessage.Action.ResponsesShellToolCallAction = &copyAction
			}

			if original.ResponsesToolMessage.Action.ResponsesMCPApprovalRequestAction != nil {
				copyAction := *original.ResponsesToolMessage.Action.ResponsesMCPApprovalRequestAction
				copy.ResponsesToolMessage.Action.ResponsesMCPApprovalRequestAction = &copyAction
			}
		}

		if original.ResponsesToolMessage.Caller != nil {
			copyCaller := *original.ResponsesToolMessage.Caller
			if original.ResponsesToolMessage.Caller.ToolID != nil {
				copyToolID := *original.ResponsesToolMessage.Caller.ToolID
				copyCaller.ToolID = &copyToolID
			}
			copy.ResponsesToolMessage.Caller = &copyCaller
		}

		if original.ResponsesToolMessage.ItemCaller != nil {
			itemCaller := *original.ResponsesToolMessage.ItemCaller
			if original.ResponsesToolMessage.ItemCaller.CallerID != nil {
				callerID := *original.ResponsesToolMessage.ItemCaller.CallerID
				itemCaller.CallerID = &callerID
			}
			copy.ResponsesToolMessage.ItemCaller = &itemCaller
		}

		if original.ResponsesToolMessage.CreatedBy != nil {
			createdBy := *original.ResponsesToolMessage.CreatedBy
			copy.ResponsesToolMessage.CreatedBy = &createdBy
		}

		if original.ResponsesToolMessage.ResponsesShellCall != nil {
			copy.ResponsesToolMessage.ResponsesShellCall = deepCopyShellCall(original.ResponsesToolMessage.ResponsesShellCall)
		}

		if original.ResponsesToolMessage.ResponsesApplyPatchCall != nil {
			copy.ResponsesToolMessage.ResponsesApplyPatchCall = deepCopyApplyPatchCall(original.ResponsesToolMessage.ResponsesApplyPatchCall)
		}

		if original.ResponsesToolMessage.ResponsesFileSearchToolCall != nil {
			copyToolCall := *original.ResponsesToolMessage.ResponsesFileSearchToolCall

			if copyToolCall.Queries != nil {
				copyToolCall.Queries = make([]string, len(copyToolCall.Queries))
				for i, query := range original.ResponsesToolMessage.ResponsesFileSearchToolCall.Queries {
					copyToolCall.Queries[i] = query
				}
			}
			copy.ResponsesToolMessage.ResponsesFileSearchToolCall = &copyToolCall
		}

		if original.ResponsesToolMessage.ResponsesWebFetchCall != nil {
			copyCall := *original.ResponsesToolMessage.ResponsesWebFetchCall
			if original.ResponsesToolMessage.ResponsesWebFetchCall.Document != nil {
				docCopy := *original.ResponsesToolMessage.ResponsesWebFetchCall.Document
				if original.ResponsesToolMessage.ResponsesWebFetchCall.Document.Source != nil {
					srcCopy := *original.ResponsesToolMessage.ResponsesWebFetchCall.Document.Source
					docCopy.Source = &srcCopy
				}
				if original.ResponsesToolMessage.ResponsesWebFetchCall.Document.Citations != nil {
					citationsCopy := *original.ResponsesToolMessage.ResponsesWebFetchCall.Document.Citations
					docCopy.Citations = &citationsCopy
				}
				copyCall.Document = &docCopy
			}
			copy.ResponsesToolMessage.ResponsesWebFetchCall = &copyCall
		}

		if original.ResponsesToolMessage.ResponsesComputerToolCall != nil {
			copyToolCall := *original.ResponsesToolMessage.ResponsesComputerToolCall
			if original.ResponsesToolMessage.ResponsesComputerToolCall.PendingSafetyChecks != nil {
				copyToolCall.PendingSafetyChecks = append([]ResponsesComputerToolCallPendingSafetyCheck(nil), original.ResponsesToolMessage.ResponsesComputerToolCall.PendingSafetyChecks...)
			}
			if actions := original.ResponsesToolMessage.ResponsesComputerToolCall.Actions; actions != nil {
				copyToolCall.Actions = make([]ResponsesComputerToolCallAction, len(actions))
				for i := range actions {
					copyToolCall.Actions[i] = deepCopyComputerToolCallAction(actions[i])
				}
			}
			copy.ResponsesToolMessage.ResponsesComputerToolCall = &copyToolCall
		}

		if original.ResponsesToolMessage.ResponsesComputerToolCallOutput != nil {
			copyToolCallOutput := *original.ResponsesToolMessage.ResponsesComputerToolCallOutput
			if original.ResponsesToolMessage.ResponsesComputerToolCallOutput.AcknowledgedSafetyChecks != nil {
				copyToolCallOutput.AcknowledgedSafetyChecks = make([]ResponsesComputerToolCallAcknowledgedSafetyCheck, len(original.ResponsesToolMessage.ResponsesComputerToolCallOutput.AcknowledgedSafetyChecks))
				for i, safetyCheck := range original.ResponsesToolMessage.ResponsesComputerToolCallOutput.AcknowledgedSafetyChecks {
					copySafetyCheck := ResponsesComputerToolCallAcknowledgedSafetyCheck{ID: safetyCheck.ID}
					if safetyCheck.Code != nil {
						copyCode := *safetyCheck.Code
						copySafetyCheck.Code = &copyCode
					}
					if safetyCheck.Message != nil {
						copyMessage := *safetyCheck.Message
						copySafetyCheck.Message = &copyMessage
					}
					copyToolCallOutput.AcknowledgedSafetyChecks[i] = copySafetyCheck
				}
			}
			copy.ResponsesToolMessage.ResponsesComputerToolCallOutput = &copyToolCallOutput
		}

		if original.ResponsesToolMessage.ResponsesCodeInterpreterToolCall != nil {
			copyToolCall := *original.ResponsesToolMessage.ResponsesCodeInterpreterToolCall
			if original.ResponsesToolMessage.ResponsesCodeInterpreterToolCall.Code != nil {
				copyCode := *original.ResponsesToolMessage.ResponsesCodeInterpreterToolCall.Code
				copyToolCall.Code = &copyCode
			}
			if original.ResponsesToolMessage.ResponsesCodeInterpreterToolCall.Outputs != nil {
				copyToolCall.Outputs = make([]ResponsesCodeInterpreterOutput, len(original.ResponsesToolMessage.ResponsesCodeInterpreterToolCall.Outputs))
				for i, output := range original.ResponsesToolMessage.ResponsesCodeInterpreterToolCall.Outputs {
					copyOutput := ResponsesCodeInterpreterOutput{}
					if output.ResponsesCodeInterpreterOutputLogs != nil {
						copyLogs := *output.ResponsesCodeInterpreterOutputLogs
						copyOutput.ResponsesCodeInterpreterOutputLogs = &copyLogs
					}
					if output.ResponsesCodeInterpreterOutputImage != nil {
						copyImage := *output.ResponsesCodeInterpreterOutputImage
						copyOutput.ResponsesCodeInterpreterOutputImage = &copyImage
					}
					copyToolCall.Outputs[i] = copyOutput
				}
			}
			copy.ResponsesToolMessage.ResponsesCodeInterpreterToolCall = &copyToolCall
		}

		if original.ResponsesToolMessage.ServerLabel != nil {
			serverLabel := *original.ResponsesToolMessage.ServerLabel
			copy.ResponsesToolMessage.ServerLabel = &serverLabel
		}

		if original.ResponsesToolMessage.ResponsesImageGenerationCall != nil {
			copyToolCall := *original.ResponsesToolMessage.ResponsesImageGenerationCall
			if original.ResponsesToolMessage.ResponsesImageGenerationCall.Background != nil {
				copyBackground := *original.ResponsesToolMessage.ResponsesImageGenerationCall.Background
				copyToolCall.Background = &copyBackground
			}
			if original.ResponsesToolMessage.ResponsesImageGenerationCall.OutputFormat != nil {
				copyOutputFormat := *original.ResponsesToolMessage.ResponsesImageGenerationCall.OutputFormat
				copyToolCall.OutputFormat = &copyOutputFormat
			}
			if original.ResponsesToolMessage.ResponsesImageGenerationCall.Quality != nil {
				copyQuality := *original.ResponsesToolMessage.ResponsesImageGenerationCall.Quality
				copyToolCall.Quality = &copyQuality
			}
			if original.ResponsesToolMessage.ResponsesImageGenerationCall.RevisedPrompt != nil {
				copyRevisedPrompt := *original.ResponsesToolMessage.ResponsesImageGenerationCall.RevisedPrompt
				copyToolCall.RevisedPrompt = &copyRevisedPrompt
			}
			if original.ResponsesToolMessage.ResponsesImageGenerationCall.Size != nil {
				copySize := *original.ResponsesToolMessage.ResponsesImageGenerationCall.Size
				copyToolCall.Size = &copySize
			}
			copy.ResponsesToolMessage.ResponsesImageGenerationCall = &copyToolCall
		}

		if original.ResponsesToolMessage.ResponsesMCPListTools != nil {
			copyToolList := *original.ResponsesToolMessage.ResponsesMCPListTools
			if original.ResponsesToolMessage.ResponsesMCPListTools.Tools != nil {
				copyToolList.Tools = make([]ResponsesMCPTool, len(original.ResponsesToolMessage.ResponsesMCPListTools.Tools))
				for i, tool := range original.ResponsesToolMessage.ResponsesMCPListTools.Tools {
					copyTool := tool
					if tool.InputSchema != nil {
						copyTool.InputSchema = DeepCopy(tool.InputSchema).(map[string]any)
					}
					if tool.Description != nil {
						copyDescription := *tool.Description
						copyTool.Description = &copyDescription
					}
					if tool.Annotations != nil {
						var copyAnnotations map[string]any
						if *tool.Annotations != nil {
							copyAnnotations = DeepCopy(*tool.Annotations).(map[string]any)
						}
						copyTool.Annotations = &copyAnnotations
					}
					copyToolList.Tools[i] = copyTool
				}
			}
			copy.ResponsesToolMessage.ResponsesMCPListTools = &copyToolList
		}

		if original.ResponsesToolMessage.ResponsesMCPApprovalResponse != nil {
			copyApprovalResponse := *original.ResponsesToolMessage.ResponsesMCPApprovalResponse
			if original.ResponsesToolMessage.ResponsesMCPApprovalResponse.Reason != nil {
				copyReason := *original.ResponsesToolMessage.ResponsesMCPApprovalResponse.Reason
				copyApprovalResponse.Reason = &copyReason
			}
			copy.ResponsesToolMessage.ResponsesMCPApprovalResponse = &copyApprovalResponse
		}

		if original.ResponsesToolMessage.ResponsesAdvisorCall != nil {
			copyAdvisorCall := *original.ResponsesToolMessage.ResponsesAdvisorCall
			if original.ResponsesToolMessage.ResponsesAdvisorCall.Text != nil {
				copyText := *original.ResponsesToolMessage.ResponsesAdvisorCall.Text
				copyAdvisorCall.Text = &copyText
			}
			if original.ResponsesToolMessage.ResponsesAdvisorCall.EncryptedContent != nil {
				copyEncryptedContent := *original.ResponsesToolMessage.ResponsesAdvisorCall.EncryptedContent
				copyAdvisorCall.EncryptedContent = &copyEncryptedContent
			}
			if original.ResponsesToolMessage.ResponsesAdvisorCall.ErrorCode != nil {
				copyErrorCode := *original.ResponsesToolMessage.ResponsesAdvisorCall.ErrorCode
				copyAdvisorCall.ErrorCode = &copyErrorCode
			}
			if original.ResponsesToolMessage.ResponsesAdvisorCall.StopReason != nil {
				copyStopReason := *original.ResponsesToolMessage.ResponsesAdvisorCall.StopReason
				copyAdvisorCall.StopReason = &copyStopReason
			}
			copy.ResponsesToolMessage.ResponsesAdvisorCall = &copyAdvisorCall
		}

		if original.ResponsesToolMessage.ResponsesToolSearchCall != nil {
			copyToolSearchCall := *original.ResponsesToolMessage.ResponsesToolSearchCall
			copyToolSearchCall.ToolReferences = append([]string(nil), original.ResponsesToolMessage.ResponsesToolSearchCall.ToolReferences...)
			copy.ResponsesToolMessage.ResponsesToolSearchCall = &copyToolSearchCall
		}

		if original.ResponsesToolMessage.ResponsesCodeExecutionCall != nil {
			copyCodeExecutionCall := *original.ResponsesToolMessage.ResponsesCodeExecutionCall
			if original.ResponsesToolMessage.ResponsesCodeExecutionCall.Input != nil {
				copyInput := *original.ResponsesToolMessage.ResponsesCodeExecutionCall.Input
				copyCodeExecutionCall.Input = &copyInput
			}
			if original.ResponsesToolMessage.ResponsesCodeExecutionCall.Stdout != nil {
				copyStdout := *original.ResponsesToolMessage.ResponsesCodeExecutionCall.Stdout
				copyCodeExecutionCall.Stdout = &copyStdout
			}
			if original.ResponsesToolMessage.ResponsesCodeExecutionCall.Stderr != nil {
				copyStderr := *original.ResponsesToolMessage.ResponsesCodeExecutionCall.Stderr
				copyCodeExecutionCall.Stderr = &copyStderr
			}
			if original.ResponsesToolMessage.ResponsesCodeExecutionCall.ReturnCode != nil {
				copyReturnCode := *original.ResponsesToolMessage.ResponsesCodeExecutionCall.ReturnCode
				copyCodeExecutionCall.ReturnCode = &copyReturnCode
			}
			if original.ResponsesToolMessage.ResponsesCodeExecutionCall.EncryptedStdout != nil {
				copyEncryptedStdout := *original.ResponsesToolMessage.ResponsesCodeExecutionCall.EncryptedStdout
				copyCodeExecutionCall.EncryptedStdout = &copyEncryptedStdout
			}
			if original.ResponsesToolMessage.ResponsesCodeExecutionCall.FileType != nil {
				copyFileType := *original.ResponsesToolMessage.ResponsesCodeExecutionCall.FileType
				copyCodeExecutionCall.FileType = &copyFileType
			}
			if original.ResponsesToolMessage.ResponsesCodeExecutionCall.FileContent != nil {
				copyFileContent := *original.ResponsesToolMessage.ResponsesCodeExecutionCall.FileContent
				copyCodeExecutionCall.FileContent = &copyFileContent
			}
			if original.ResponsesToolMessage.ResponsesCodeExecutionCall.StartLine != nil {
				copyStartLine := *original.ResponsesToolMessage.ResponsesCodeExecutionCall.StartLine
				copyCodeExecutionCall.StartLine = &copyStartLine
			}
			if original.ResponsesToolMessage.ResponsesCodeExecutionCall.NumLines != nil {
				copyNumLines := *original.ResponsesToolMessage.ResponsesCodeExecutionCall.NumLines
				copyCodeExecutionCall.NumLines = &copyNumLines
			}
			if original.ResponsesToolMessage.ResponsesCodeExecutionCall.TotalLines != nil {
				copyTotalLines := *original.ResponsesToolMessage.ResponsesCodeExecutionCall.TotalLines
				copyCodeExecutionCall.TotalLines = &copyTotalLines
			}
			if original.ResponsesToolMessage.ResponsesCodeExecutionCall.IsFileUpdate != nil {
				copyIsFileUpdate := *original.ResponsesToolMessage.ResponsesCodeExecutionCall.IsFileUpdate
				copyCodeExecutionCall.IsFileUpdate = &copyIsFileUpdate
			}
			if original.ResponsesToolMessage.ResponsesCodeExecutionCall.OldStart != nil {
				copyOldStart := *original.ResponsesToolMessage.ResponsesCodeExecutionCall.OldStart
				copyCodeExecutionCall.OldStart = &copyOldStart
			}
			if original.ResponsesToolMessage.ResponsesCodeExecutionCall.OldLines != nil {
				copyOldLines := *original.ResponsesToolMessage.ResponsesCodeExecutionCall.OldLines
				copyCodeExecutionCall.OldLines = &copyOldLines
			}
			if original.ResponsesToolMessage.ResponsesCodeExecutionCall.NewStart != nil {
				copyNewStart := *original.ResponsesToolMessage.ResponsesCodeExecutionCall.NewStart
				copyCodeExecutionCall.NewStart = &copyNewStart
			}
			if original.ResponsesToolMessage.ResponsesCodeExecutionCall.NewLines != nil {
				copyNewLines := *original.ResponsesToolMessage.ResponsesCodeExecutionCall.NewLines
				copyCodeExecutionCall.NewLines = &copyNewLines
			}
			if original.ResponsesToolMessage.ResponsesCodeExecutionCall.Lines != nil {
				copyCodeExecutionCall.Lines = append([]string(nil), original.ResponsesToolMessage.ResponsesCodeExecutionCall.Lines...)
			}
			if original.ResponsesToolMessage.ResponsesCodeExecutionCall.ErrorCode != nil {
				copyErrorCode := *original.ResponsesToolMessage.ResponsesCodeExecutionCall.ErrorCode
				copyCodeExecutionCall.ErrorCode = &copyErrorCode
			}
			if original.ResponsesToolMessage.ResponsesCodeExecutionCall.Files != nil {
				copyCodeExecutionCall.Files = append([]ResponsesCodeExecutionFileOutput(nil), original.ResponsesToolMessage.ResponsesCodeExecutionCall.Files...)
			}
			if original.ResponsesToolMessage.ResponsesCodeExecutionCall.ContainerExpiresAt != nil {
				copyContainerExpiresAt := *original.ResponsesToolMessage.ResponsesCodeExecutionCall.ContainerExpiresAt
				copyCodeExecutionCall.ContainerExpiresAt = &copyContainerExpiresAt
			}
			if original.ResponsesToolMessage.ResponsesCodeExecutionCall.Caller != nil {
				copyCaller := *original.ResponsesToolMessage.ResponsesCodeExecutionCall.Caller
				if original.ResponsesToolMessage.ResponsesCodeExecutionCall.Caller.ToolID != nil {
					copyToolID := *original.ResponsesToolMessage.ResponsesCodeExecutionCall.Caller.ToolID
					copyCaller.ToolID = &copyToolID
				}
				copyCodeExecutionCall.Caller = &copyCaller
			}
			copy.ResponsesToolMessage.ResponsesCodeExecutionCall = &copyCodeExecutionCall
		}

	}

	if original.ResponsesReasoning != nil {
		copyReasoning := *original.ResponsesReasoning
		copy.ResponsesReasoning = &copyReasoning
	}

	return copy
}

func deepCopyShellCallOutput(original []ResponsesShellCallOutputContent) []ResponsesShellCallOutputContent {
	copied := make([]ResponsesShellCallOutputContent, len(original))
	for i, content := range original {
		copied[i] = content
		if content.Outcome.ExitCode != nil {
			exitCode := *content.Outcome.ExitCode
			copied[i].Outcome.ExitCode = &exitCode
		}
		if content.CreatedBy != nil {
			createdBy := *content.CreatedBy
			copied[i].CreatedBy = &createdBy
		}
	}
	return copied
}

func deepCopyShellCall(original *ResponsesShellCall) *ResponsesShellCall {
	copied := *original
	if original.Environment != nil {
		environment := *original.Environment
		if original.Environment.ContainerID != nil {
			containerID := *original.Environment.ContainerID
			environment.ContainerID = &containerID
		}
		copied.Environment = &environment
	}
	if original.MaxOutputLength != nil {
		maxOutputLength := *original.MaxOutputLength
		copied.MaxOutputLength = &maxOutputLength
	}
	return &copied
}

func deepCopyApplyPatchCall(original *ResponsesApplyPatchCall) *ResponsesApplyPatchCall {
	copied := *original
	if original.Operation != nil {
		operation := *original.Operation
		if original.Operation.Diff != nil {
			diff := *original.Operation.Diff
			operation.Diff = &diff
		}
		copied.Operation = &operation
	}
	return &copied
}

func deepCopyResponsesMessageContentBlock(original ResponsesMessageContentBlock) ResponsesMessageContentBlock {
	copy := ResponsesMessageContentBlock{
		Type: original.Type,
	}

	if original.CacheControl != nil {
		copyCacheControl := &CacheControl{Type: original.CacheControl.Type}
		if original.CacheControl.TTL != nil {
			copyTTL := *original.CacheControl.TTL
			copyCacheControl.TTL = &copyTTL
		}
		if original.CacheControl.Scope != nil {
			copyScope := *original.CacheControl.Scope
			copyCacheControl.Scope = &copyScope
		}
		copy.CacheControl = copyCacheControl
	}

	if original.Citations != nil {
		copyCitations := &Citations{}
		if original.Citations.Enabled != nil {
			copyEnabled := *original.Citations.Enabled
			copyCitations.Enabled = &copyEnabled
		}
		copy.Citations = copyCitations
	}

	if original.PromptCacheBreakpoint != nil {
		copyPromptCacheBreakpoint := &PromptCacheBreakpoint{}
		if original.PromptCacheBreakpoint.Mode != nil {
			copyMode := *original.PromptCacheBreakpoint.Mode
			copyPromptCacheBreakpoint.Mode = &copyMode
		}
		copy.PromptCacheBreakpoint = copyPromptCacheBreakpoint
	}

	if original.FileID != nil {
		copyFileID := *original.FileID
		copy.FileID = &copyFileID
	}

	if original.Text != nil {
		copyText := *original.Text
		copy.Text = &copyText
	}

	if original.ToolName != nil {
		copy.ToolName = new(*original.ToolName)
	}

	if original.Signature != nil {
		copy.Signature = new(string)
		*copy.Signature = *original.Signature
	}
	if original.EncryptedContent != nil {
		copy.EncryptedContent = new(string)
		*copy.EncryptedContent = *original.EncryptedContent
	}

	if original.MediaResolution != nil {
		copyMediaResolution := &MediaResolution{Level: original.MediaResolution.Level}
		if original.MediaResolution.NumTokens != nil {
			copyMediaResolution.NumTokens = new(*original.MediaResolution.NumTokens)
		}
		copy.MediaResolution = copyMediaResolution
	}

	if original.GuardContent != nil {
		copyGuardContent := &GuardContent{}
		if original.GuardContent.Qualifiers != nil {
			copyGuardContent.Qualifiers = append([]string(nil), original.GuardContent.Qualifiers...)
		}
		copy.GuardContent = copyGuardContent
	}

	if original.ResponsesInputMessageContentBlockImage != nil {
		copyImage := &ResponsesInputMessageContentBlockImage{}
		if original.ResponsesInputMessageContentBlockImage.ImageURL != nil {
			copyImageURL := *original.ResponsesInputMessageContentBlockImage.ImageURL
			copyImage.ImageURL = &copyImageURL
		}
		if original.ResponsesInputMessageContentBlockImage.Detail != nil {
			copyDetail := *original.ResponsesInputMessageContentBlockImage.Detail
			copyImage.Detail = &copyDetail
		}
		copy.ResponsesInputMessageContentBlockImage = copyImage
	}

	if original.ResponsesInputMessageContentBlockFile != nil {
		copyFile := &ResponsesInputMessageContentBlockFile{}
		if original.ResponsesInputMessageContentBlockFile.FileData != nil {
			copyFileData := *original.ResponsesInputMessageContentBlockFile.FileData
			copyFile.FileData = &copyFileData
		}
		if original.ResponsesInputMessageContentBlockFile.FileURL != nil {
			copyFileURL := *original.ResponsesInputMessageContentBlockFile.FileURL
			copyFile.FileURL = &copyFileURL
		}
		if original.ResponsesInputMessageContentBlockFile.Filename != nil {
			copyFilename := *original.ResponsesInputMessageContentBlockFile.Filename
			copyFile.Filename = &copyFilename
		}

		if original.ResponsesInputMessageContentBlockFile.FileType != nil {
			copyFileType := *original.ResponsesInputMessageContentBlockFile.FileType
			copyFile.FileType = &copyFileType
		}
		copy.ResponsesInputMessageContentBlockFile = copyFile
	}

	if original.Audio != nil {
		copyAudio := &ResponsesInputMessageContentBlockAudio{
			Format: original.Audio.Format,
			Data:   original.Audio.Data,
		}
		copy.Audio = copyAudio
	}

	if original.ResponsesOutputMessageContentText != nil {
		copyText := &ResponsesOutputMessageContentText{}

		if original.ResponsesOutputMessageContentText.Annotations != nil {
			copyText.Annotations = make([]ResponsesOutputMessageContentTextAnnotation, len(original.ResponsesOutputMessageContentText.Annotations))
			for i, annotation := range original.ResponsesOutputMessageContentText.Annotations {
				copyAnnotation := ResponsesOutputMessageContentTextAnnotation{
					Type: annotation.Type,
				}
				if annotation.Index != nil {
					copyIndex := *annotation.Index
					copyAnnotation.Index = &copyIndex
				}
				if annotation.FileID != nil {
					copyFileID := *annotation.FileID
					copyAnnotation.FileID = &copyFileID
				}
				if annotation.Text != nil {
					copyText := *annotation.Text
					copyAnnotation.Text = &copyText
				}
				if annotation.StartIndex != nil {
					copyStartIndex := *annotation.StartIndex
					copyAnnotation.StartIndex = &copyStartIndex
				}
				if annotation.EndIndex != nil {
					copyEndIndex := *annotation.EndIndex
					copyAnnotation.EndIndex = &copyEndIndex
				}
				if annotation.Filename != nil {
					copyFilename := *annotation.Filename
					copyAnnotation.Filename = &copyFilename
				}
				if annotation.Title != nil {
					copyTitle := *annotation.Title
					copyAnnotation.Title = &copyTitle
				}
				if annotation.URL != nil {
					copyURL := *annotation.URL
					copyAnnotation.URL = &copyURL
				}
				if annotation.ContainerID != nil {
					copyContainerID := *annotation.ContainerID
					copyAnnotation.ContainerID = &copyContainerID
				}

				if annotation.StartCharIndex != nil {
					copyStartCharIndex := *annotation.StartCharIndex
					copyAnnotation.StartCharIndex = &copyStartCharIndex
				}
				if annotation.EndCharIndex != nil {
					copyEndCharIndex := *annotation.EndCharIndex
					copyAnnotation.EndCharIndex = &copyEndCharIndex
				}
				if annotation.StartPageNumber != nil {
					copyStartPageNumber := *annotation.StartPageNumber
					copyAnnotation.StartPageNumber = &copyStartPageNumber
				}
				if annotation.EndPageNumber != nil {
					copyEndPageNumber := *annotation.EndPageNumber
					copyAnnotation.EndPageNumber = &copyEndPageNumber
				}
				if annotation.StartBlockIndex != nil {
					copyStartBlockIndex := *annotation.StartBlockIndex
					copyAnnotation.StartBlockIndex = &copyStartBlockIndex
				}
				if annotation.EndBlockIndex != nil {
					copyEndBlockIndex := *annotation.EndBlockIndex
					copyAnnotation.EndBlockIndex = &copyEndBlockIndex
				}
				if annotation.Source != nil {
					copySource := *annotation.Source
					copyAnnotation.Source = &copySource
				}
				if annotation.EncryptedIndex != nil {
					copyEncryptedIndex := *annotation.EncryptedIndex
					copyAnnotation.EncryptedIndex = &copyEncryptedIndex
				}
				copyText.Annotations[i] = copyAnnotation
			}
		}

		if original.ResponsesOutputMessageContentText.LogProbs != nil {
			copyText.LogProbs = make([]ResponsesOutputMessageContentTextLogProb, len(original.ResponsesOutputMessageContentText.LogProbs))
			for i, logProb := range original.ResponsesOutputMessageContentText.LogProbs {
				copyLogProb := ResponsesOutputMessageContentTextLogProb{
					LogProb: logProb.LogProb,
					Token:   logProb.Token,
				}

				if logProb.Bytes != nil {
					copyLogProb.Bytes = make([]int, len(logProb.Bytes))
					for k, b := range logProb.Bytes {
						copyLogProb.Bytes[k] = b
					}
				}

				if logProb.TopLogProbs != nil {
					copyLogProb.TopLogProbs = make([]LogProb, len(logProb.TopLogProbs))
					for j, topLogProb := range logProb.TopLogProbs {
						copyTopLogProb := LogProb{
							LogProb: topLogProb.LogProb,
							Token:   topLogProb.Token,
						}

						if topLogProb.Bytes != nil {
							copyTopLogProb.Bytes = make([]int, len(topLogProb.Bytes))
							for k, b := range topLogProb.Bytes {
								copyTopLogProb.Bytes[k] = b
							}
						}
						copyLogProb.TopLogProbs[j] = copyTopLogProb
					}
				}
				copyText.LogProbs[i] = copyLogProb
			}
		}

		copy.ResponsesOutputMessageContentText = copyText
	}

	if original.ResponsesOutputMessageContentRefusal != nil {
		copyRefusal := &ResponsesOutputMessageContentRefusal{
			Refusal: original.ResponsesOutputMessageContentRefusal.Refusal,
		}
		copy.ResponsesOutputMessageContentRefusal = copyRefusal
	}

	if original.ResponsesOutputMessageContentRenderedContent != nil {
		copy.ResponsesOutputMessageContentRenderedContent = &ResponsesOutputMessageContentRenderedContent{
			RenderedContent: original.ResponsesOutputMessageContentRenderedContent.RenderedContent,
		}
	}
	if original.ResponsesOutputMessageContentCompaction != nil {
		copyCompaction := &ResponsesOutputMessageContentCompaction{
			Summary: original.ResponsesOutputMessageContentCompaction.Summary,
		}

		if changes := original.ResponsesOutputMessageContentCompaction.ToolChanges; changes != nil {
			copyCompaction.ToolChanges = make([]ResponsesMessageContentBlock, len(changes))
			for i, change := range changes {
				copyCompaction.ToolChanges[i] = deepCopyResponsesMessageContentBlock(change)
			}
		}
		copy.ResponsesOutputMessageContentCompaction = copyCompaction
	}

	if original.ToolChange != nil {
		copyTarget := &ResponsesToolChangeTarget{Type: original.ToolChange.Type}
		if original.ToolChange.Name != nil {
			copyTarget.Name = new(*original.ToolChange.Name)
		}
		if original.ToolChange.ServerName != nil {
			copyTarget.ServerName = new(*original.ToolChange.ServerName)
		}
		if original.ToolChange.Definition != nil {
			copyDefinition := *original.ToolChange.Definition
			copyTarget.Definition = &copyDefinition
		}
		copy.ToolChange = copyTarget
	}
	if original.ResponsesOutputMessageContentFallback != nil {
		copyFallback := &ResponsesOutputMessageContentFallback{
			FromModel:   original.ResponsesOutputMessageContentFallback.FromModel,
			ToModel:     original.ResponsesOutputMessageContentFallback.ToModel,
			TriggerType: original.ResponsesOutputMessageContentFallback.TriggerType,
		}
		if original.ResponsesOutputMessageContentFallback.TriggerCategory != nil {
			copyTriggerCategory := *original.ResponsesOutputMessageContentFallback.TriggerCategory
			copyFallback.TriggerCategory = &copyTriggerCategory
		}
		copy.ResponsesOutputMessageContentFallback = copyFallback
	}

	return copy
}

func IsNovaModel(model string) bool {
	return strings.Contains(model, "nova")
}

func IsDeepSeekModel(model string) bool {
	return strings.Contains(strings.ToLower(model), "deepseek")
}

func IsMoonshotModel(model string) bool {
	for _, segment := range strings.FieldsFunc(strings.ToLower(model), func(r rune) bool {
		return r == '/' || r == '.' || r == ':'
	}) {
		if segment == "moonshotai" || strings.HasPrefix(segment, "kimi-") {
			return true
		}
	}
	return false
}

func IsGPT56Model(model string) bool {
	const needle = "gpt-5.6"
	lower := strings.ToLower(model)
	for start := 0; start < len(lower); {
		idx := strings.Index(lower[start:], needle)
		if idx < 0 {
			return false
		}
		end := start + idx + len(needle)
		if end == len(lower) || lower[end] < '0' || lower[end] > '9' {
			return true
		}

		start = end
	}
	return false
}

func ModelSupportsPromptCacheBreakpoint(model string) bool {
	return IsGPT56Model(model) || strings.Contains(strings.ToLower(model), "gpt-6")
}

func IsAnthropicModel(model string) bool {
	return strings.Contains(model, "anthropic.") || strings.Contains(model, "claude")
}

func IsOpenAIModel(model string) bool {
	if strings.Contains(model, "gpt-") || strings.Contains(model, "text-embedding-") {
		return true
	}

	return isOSeriesModel(model)
}

func isOSeriesModel(model string) bool {
	name := model
	if idx := strings.LastIndexAny(name, "/:"); idx >= 0 {
		name = name[idx+1:]
	}
	if len(name) < 2 || name[0] != 'o' || name[1] < '0' || name[1] > '9' {
		return false
	}
	return len(name) == 2 || name[2] == '-'
}

func BedrockModelSupportsCachePoints(model string) bool {
	return IsAnthropicModel(model) || IsNovaModel(model)
}

func ResolveBedrockMantleBasePath(model string) BedrockMantleBasePath {
	fallback := BedrockMantleBasePathV1
	lower := strings.ToLower(model)
	if strings.Contains(lower, "gpt-5") || strings.Contains(lower, "gpt-6") ||
		strings.Contains(lower, "gemma-4") || IsGrokModel(model) {
		fallback = BedrockMantleBasePathOpenAIV1
	}
	return ResolveModelCaps(BedrockMantle, model).BedrockMantleBasePath(fallback)
}

func ModelSupportsPromptCaching(provider ModelProvider, model string) bool {
	switch provider {
	case Anthropic, OpenRouter:
		return IsAnthropicModel(model)
	case Bedrock, BedrockMantle:
		return BedrockModelSupportsCachePoints(model) || ModelSupportsPromptCacheBreakpoint(model)
	case Vertex:

		return IsAnthropicModel(model)
	case Azure, OpenAI:
		return ModelSupportsPromptCacheBreakpoint(model)
	default:
		return false
	}
}

func IsMistralModel(model string) bool {
	return strings.Contains(model, "mistral") || strings.Contains(model, "codestral")
}

func IsFable51(model string) bool {
	m := strings.ToLower(model)
	if !strings.Contains(m, "fable") && !strings.Contains(m, "mythos") {
		return false
	}
	return strings.Contains(m, "5-1") || strings.Contains(m, "5.1")
}

func IsOpus55Plus(model string) bool {
	m := strings.ToLower(model)
	if !strings.Contains(m, "opus") {
		return false
	}
	return strings.Contains(m, "5-5") || strings.Contains(m, "5.5")
}

func DefaultSupportsDecisions(model string) bool {
	model = strings.TrimPrefix(strings.ToLower(model), "openai/")
	for _, prefix := range openAIDecisionModelPrefixes {
		if strings.HasPrefix(model, prefix) {
			return true
		}
	}
	return false
}

func DefaultSupportsForcedToolChoice(model string) bool {
	return !IsFable51(model) && !IsOpus55Plus(model)
}

func IsLlamaModel(model string) bool {
	return strings.Contains(model, "llama")
}

func IsGeminiModel(model string) bool {
	return strings.Contains(model, "gemini")
}

func IsVeoModel(model string) bool {
	return strings.Contains(model, "veo")
}

func IsGemmaModel(model string) bool {
	return strings.Contains(model, "gemma")
}

func IsImagenModel(model string) bool {
	return strings.Contains(strings.ToLower(model), "imagen")
}

func IsCohereModel(model string) bool {
	return strings.Contains(model, "cohere")
}

func IsTitanModel(model string) bool {
	return strings.Contains(model, "titan")
}

func IsGrokModel(model string) bool {
	return strings.Contains(model, "grok")
}

func normalizeGrokModel(model string) string {
	m := strings.ToLower(strings.TrimSpace(model))

	if idx := strings.LastIndex(m, "/"); idx != -1 {
		m = m[idx+1:]
	}
	m = strings.TrimSuffix(m, "-latest")
	return grokDateSuffixRe.ReplaceAllString(m, "")
}

func SupportsGrokReasoningEffort(model string) bool {
	_, denied := grokModelsWithoutReasoningEffort[normalizeGrokModel(model)]
	return !denied
}

func SupportsGrokXHighReasoningEffort(model string) bool {
	_, ok := grokModelsWithXHighReasoningEffort[normalizeGrokModel(model)]
	return ok
}

func IsGrokReasoningModel(model string) bool {

	for _, reasoningModel := range grokReasoningModels {
		if strings.Contains(model, reasoningModel) {

			if strings.Contains(model, "non-reasoning") {
				return false
			}
			return true
		}
	}
	return false
}

func SplitModelAndVersion(id string) (base, version string) {
	if id == "" {
		return "", ""
	}

	parts := strings.Split(id, "-")
	n := len(parts)
	if n == 0 {
		return "", ""
	}

	if n >= 3 {
		last3 := strings.Join(parts[n-3:], "-")
		if openAIDateRe.MatchString(last3) {
			base := strings.Join(parts[:n-3], "-")
			return base, last3
		}
	}

	if n >= 2 {
		last := parts[n-1]
		if anthropicDateRe.MatchString(last) || taggedVersionRe.MatchString(last) {
			base := strings.Join(parts[:n-1], "-")
			return base, last
		}
	}

	return id, ""
}

func BaseModelName(id string) string {
	base, _ := SplitModelAndVersion(id)
	return base
}
