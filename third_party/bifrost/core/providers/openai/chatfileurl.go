// Auriga modifications: trimmed relay core/providers; see third_party/bifrost/UPSTREAM.md.

package openai

import (
	"context"
	"encoding/base64"
	"fmt"
	"mime"
	"net/http"
	"net/url"
	"path"
	"strings"

	providerUtils "github.com/maximhq/bifrost/core/providers/utils"
	"github.com/maximhq/bifrost/core/schemas"
)

var mediaTypeExtensionAliases = map[string][]string{
	"text/html":     {".htm"},
	"text/markdown": {".markdown"},
}
var mediaTypeExtensions = map[string]string{
	"application/pdf":  ".pdf",
	"text/plain":       ".txt",
	"text/markdown":    ".md",
	"text/html":        ".html",
	"text/csv":         ".csv",
	"application/json": ".json",
}

func buildChatFileFromFetch(mediaType, encoded, sourceURL string) (fileData string, filename string) {
	if parsed, _, err := mime.ParseMediaType(mediaType); err == nil && parsed != "" {
		mediaType = parsed
	}
	mediaType = strings.ToLower(strings.TrimSpace(mediaType))
	if mediaType == "" {
		mediaType = "application/octet-stream"
	}

	fileData = fmt.Sprintf("data:%s;base64,%s", mediaType, encoded)

	ext := mediaTypeExtensions[mediaType]

	if parsed, err := url.Parse(sourceURL); err == nil {
		if base := path.Base(parsed.Path); base != "" && base != "." && base != "/" && strings.Contains(base, ".") {
			if extensionMatchesMediaType(path.Ext(base), mediaType) {
				return fileData, base
			}

			if ext != "" {
				return fileData, strings.TrimSuffix(base, path.Ext(base)) + ext
			}
		}
	}
	if ext == "" {
		ext = ".bin"
	}
	return fileData, "document" + ext
}

func extensionMatchesMediaType(ext, mediaType string) bool {
	canonical, known := mediaTypeExtensions[mediaType]
	if !known {
		return true
	}
	ext = strings.ToLower(ext)
	if ext == canonical {
		return true
	}
	for _, alias := range mediaTypeExtensionAliases[mediaType] {
		if ext == alias {
			return true
		}
	}
	return false
}

func ResolveChatFileURLs(ctx *schemas.BifrostContext, provider schemas.ModelProvider, req *OpenAIChatRequest) error {
	if req == nil || (provider != schemas.OpenAI && provider != schemas.Azure) {
		return nil
	}

	var fetchCtx context.Context = ctx
	if ctx == nil {
		fetchCtx = context.Background()
	}

	messages := req.Messages
	for i := range messages {
		msg := &messages[i]
		if msg.Content == nil || msg.Content.ContentBlocks == nil {
			continue
		}
		for j := range msg.Content.ContentBlocks {
			file := msg.Content.ContentBlocks[j].File
			if file == nil || file.FileURL == nil || *file.FileURL == "" {
				continue
			}
			if file.FileID != nil && *file.FileID != "" {
				continue
			}
			if file.FileData != nil && *file.FileData != "" {
				continue
			}

			if scheme := urlSourceScheme(*file.FileURL); scheme != "http" && scheme != "https" {
				continue
			}

			mediaType, encoded, err := providerUtils.FetchAndEncodeURL(fetchCtx, *file.FileURL)
			if err != nil {
				return fmt.Errorf("failed to fetch document at %s for messages[%d].content[%d]: %w",
					*file.FileURL, i, j, err)
			}

			fileData, filename := buildChatFileFromFetch(mediaType, encoded, *file.FileURL)
			fileCopy := *file
			fileCopy.FileData = schemas.Ptr(fileData)
			if fileCopy.Filename == nil || *fileCopy.Filename == "" {
				fileCopy.Filename = schemas.Ptr(filename)
			}
			fileCopy.FileURL = nil
			msg.Content.ContentBlocks[j].File = &fileCopy
		}
	}
	return nil
}

func urlSourceScheme(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	return strings.ToLower(parsed.Scheme)
}

func fileDataAsDataURL(fileData string, fileType *string) string {
	if fileData == "" || strings.HasPrefix(fileData, "data:") {
		return fileData
	}

	mediaType := ""
	if fileType != nil {
		mediaType = normalizeFileType(*fileType)
	}
	if mediaType == "text/plain" {
		return "data:text/plain;base64," + base64.StdEncoding.EncodeToString([]byte(fileData))
	}

	decoded, err := base64.StdEncoding.DecodeString(fileData)
	if err != nil {
		return fileData
	}
	if mediaType == "" {
		mediaType = sniffFileMediaType(decoded)
	}
	return "data:" + mediaType + ";base64," + fileData
}

func normalizeFileType(fileType string) string {
	fileType = strings.ToLower(strings.TrimSpace(fileType))
	if fileType == "" {
		return ""
	}
	if strings.Contains(fileType, "/") {
		if parsed, _, err := mime.ParseMediaType(fileType); err == nil && parsed != "" {
			return parsed
		}
		return fileType
	}
	if fileType == "txt" {
		return "text/plain"
	}
	if byExt := mime.TypeByExtension("." + fileType); byExt != "" {
		if parsed, _, err := mime.ParseMediaType(byExt); err == nil && parsed != "" {
			return parsed
		}
	}
	return ""
}

func sniffFileMediaType(data []byte) string {
	detected := http.DetectContentType(data)
	if parsed, _, err := mime.ParseMediaType(detected); err == nil {
		detected = parsed
	}
	if detected == "" || detected == "application/octet-stream" {
		return "application/pdf"
	}
	return detected
}

func fileDataNeedsDataURL(fileData *string) bool {
	return fileData != nil && *fileData != "" && !strings.HasPrefix(*fileData, "data:")
}
