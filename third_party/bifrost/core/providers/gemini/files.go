package gemini

import (
	"strings"

	"github.com/maximhq/bifrost/core/schemas"
)

type GeminiFileVideoMetadata struct {
	VideoDuration string `json:"videoDuration"`
}
type GeminiFileListResponse struct {
	Files         []GeminiFileResponse `json:"files"`
	NextPageToken string               `json:"nextPageToken,omitempty"`
}
type GeminiFileResponse struct {
	Name           string                   `json:"name"`
	DisplayName    string                   `json:"displayName"`
	MimeType       string                   `json:"mimeType"`
	SizeBytes      string                   `json:"sizeBytes"`
	CreateTime     string                   `json:"createTime"`
	UpdateTime     string                   `json:"updateTime"`
	ExpirationTime string                   `json:"expirationTime,omitempty"`
	SHA256Hash     string                   `json:"sha256Hash"`
	URI            string                   `json:"uri"`
	State          string                   `json:"state"`
	VideoMetadata  *GeminiFileVideoMetadata `json:"videoMetadata,omitempty"`
}

func ToBifrostFileStatus(state string) schemas.FileStatus {
	switch state {
	case "PROCESSING":
		return schemas.FileStatusProcessing
	case "ACTIVE":
		return schemas.FileStatusProcessed
	case "FAILED":
		return schemas.FileStatusError
	default:
		return schemas.FileStatus(strings.ToLower(state))
	}
}
