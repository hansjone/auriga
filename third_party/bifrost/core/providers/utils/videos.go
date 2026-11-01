package utils

import (
	"net/url"
	"strings"

	schemas "github.com/maximhq/bifrost/core/schemas"
)

func StripVideoIDProviderSuffix(videoID string, provider schemas.ModelProvider) string {
	suffix := ":" + string(provider)
	stripped := strings.TrimSuffix(videoID, suffix)

	if decoded, err := url.PathUnescape(stripped); err == nil {
		return decoded
	}
	return stripped
}

func AddVideoIDProviderSuffix(videoID string, provider schemas.ModelProvider) string {
	if videoID == "" {
		return videoID
	}
	suffix := ":" + string(provider)
	if strings.HasSuffix(videoID, suffix) {
		return videoID
	}

	escapedVideoID := url.PathEscape(videoID)
	return escapedVideoID + suffix
}
