package anthropic

import (
	"encoding/base64"
	"fmt"
	"mime"
	"net/url"
	"path"
	"strings"

	providerUtils "github.com/maximhq/bifrost/core/providers/utils"
	schemas "github.com/maximhq/bifrost/core/schemas"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

type urlSourceRef struct {
	path      string
	url       string
	blockType string
}

func InlineURLContentSources(ctx *schemas.BifrostContext, body []byte) ([]byte, error) {
	refs := collectURLSourceRefs(body)
	if len(refs) == 0 {
		return body, nil
	}

	for _, ref := range refs {

		if scheme := urlScheme(ref.url); scheme != "http" && scheme != "https" {
			continue
		}
		mediaType, encoded, err := providerUtils.FetchAndEncodeURL(ctx, ref.url)
		if err != nil {
			return nil, fmt.Errorf("failed to inline URL content source %q: %w", providerUtils.RedactURLForError(ref.url), err)
		}
		body, err = applyInlinedSource(body, ref, mediaType, encoded)
		if err != nil {
			return nil, err
		}
	}

	return body, nil
}

func urlScheme(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	return strings.ToLower(parsed.Scheme)
}

func applyInlinedSource(body []byte, ref urlSourceRef, mediaType, encoded string) ([]byte, error) {

	if parsed, _, err := mime.ParseMediaType(mediaType); err == nil {
		mediaType = strings.ToLower(parsed)
	}
	if mediaType == "" || mediaType == "application/octet-stream" {
		mediaType = mediaTypeFromURL(ref.url, ref.blockType)
	}

	source, err := buildInlineSource(mediaType, encoded)
	if err != nil {
		return nil, fmt.Errorf("failed to inline URL content source %q: %w", providerUtils.RedactURLForError(ref.url), err)
	}

	body, err = sjson.SetBytes(body, ref.path, source)
	if err != nil {
		return nil, fmt.Errorf("failed to inline URL content source %q: %w", providerUtils.RedactURLForError(ref.url), err)
	}
	return body, nil
}

func collectURLSourceRefs(body []byte) []urlSourceRef {
	messages := gjson.GetBytes(body, "messages")
	if !messages.IsArray() {
		return nil
	}

	var refs []urlSourceRef
	messages.ForEach(func(msgIdx, message gjson.Result) bool {
		content := message.Get("content")
		if !content.IsArray() {
			return true
		}
		content.ForEach(func(blockIdx, block gjson.Result) bool {
			source := block.Get("source")
			if !source.Exists() || source.Get("type").String() != "url" {
				return true
			}
			resourceURL := source.Get("url").String()
			if resourceURL == "" {
				return true
			}
			refs = append(refs, urlSourceRef{
				path:      fmt.Sprintf("messages.%d.content.%d.source", msgIdx.Int(), blockIdx.Int()),
				url:       resourceURL,
				blockType: block.Get("type").String(),
			})
			return true
		})
		return true
	})
	return refs
}

func buildInlineSource(mediaType, encoded string) (map[string]any, error) {
	if strings.HasPrefix(mediaType, "text/") {
		decoded, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			return nil, fmt.Errorf("decoding fetched text content: %w", err)
		}
		return map[string]any{
			"type": "text",

			"media_type": "text/plain",
			"data":       string(decoded),
		}, nil
	}

	return map[string]any{
		"type":       "base64",
		"media_type": mediaType,
		"data":       encoded,
	}, nil
}

func mediaTypeFromURL(resourceURL, blockType string) string {
	if parsed, err := url.Parse(resourceURL); err == nil {
		if ext := path.Ext(parsed.Path); ext != "" {
			if byExt, _, err := mime.ParseMediaType(mime.TypeByExtension(ext)); err == nil && byExt != "" {
				return strings.ToLower(byExt)
			}
		}
	}
	if blockType == "document" {
		return "application/pdf"
	}
	return "application/octet-stream"
}
