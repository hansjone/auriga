// Auriga modifications: trimmed relay core/providers; see third_party/bifrost/UPSTREAM.md.

package schemas

import "strings"

type LLMPlugin interface {
	BasePlugin

	PreRequestHook(ctx *BifrostContext, req *BifrostRequest) error

	PreLLMHook(ctx *BifrostContext, req *BifrostRequest) (*BifrostRequest, *LLMPluginShortCircuit, error)
	PostLLMHook(ctx *BifrostContext, resp *BifrostResponse, bifrostErr *BifrostError) (*BifrostResponse, *BifrostError, error)
}
type BasePlugin interface {
	GetName() string

	Cleanup() error
}
type HTTPResponseMetadata struct {
	StatusCode int               `json:"status_code"`
	Headers    map[string]string `json:"headers"`
}
type StreamInterceptionError struct {
	BifrostError *BifrostError
}

func caseInsensitiveLookup(data map[string]string, key string) string {
	if data == nil || key == "" {
		return ""
	}

	if v, ok := data[key]; ok {
		return v
	}

	lowerKey := strings.ToLower(key)
	if v, ok := data[lowerKey]; ok {
		return v
	}

	for k, v := range data {
		if strings.EqualFold(k, key) {
			return v
		}
	}
	return ""
}

func (resp *HTTPResponseMetadata) Header(key string) string {
	if resp == nil {
		return ""
	}
	return caseInsensitiveLookup(resp.Headers, key)
}

func (e *StreamInterceptionError) Error() string {
	if e != nil && e.BifrostError != nil && e.BifrostError.Error != nil && e.BifrostError.Error.Message != "" {
		return e.BifrostError.Error.Message
	}
	return "stream interception failed"
}
