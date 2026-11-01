// Auriga modifications: trimmed relay core/providers; see third_party/bifrost/UPSTREAM.md.

package anthropic

import (
	"encoding/base64"
	"fmt"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
	"mime"
	"strings"
	"unicode/utf8"
)

func inlineTextDataURL(data string) *AnthropicSource {
	header, encoded, ok := strings.Cut(data, ",")
	if !ok || !strings.HasPrefix(header, "data:") || !strings.HasSuffix(header, ";base64") {
		return nil
	}
	media, _, err := mime.ParseMediaType(strings.TrimSuffix(strings.TrimPrefix(header, "data:"), ";base64"))
	if err != nil || !isTextDocumentMediaType(media) {
		return nil
	}
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || !utf8.Valid(decoded) {
		return nil
	}
	return &AnthropicSource{Type: "text", MediaType: schemas.Ptr("text/plain"), Data: schemas.Ptr(string(decoded))}
}

func normalizeBase64TextSources(body []byte) ([]byte, error) {
	messages := gjson.GetBytes(body, "messages")
	if !messages.IsArray() {
		return body, nil
	}

	var rebuiltMessages [][]byte
	anyChanged := false
	var convErr error

	messages.ForEach(func(_, message gjson.Result) bool {
		messageRaw := []byte(message.Raw)
		content := message.Get("content")
		if !content.IsArray() {
			rebuiltMessages = append(rebuiltMessages, messageRaw)
			return true
		}

		var rebuiltBlocks [][]byte
		messageChanged := false
		content.ForEach(func(_, block gjson.Result) bool {
			blockRaw := []byte(block.Raw)
			if block.Get("type").String() != "document" ||
				block.Get("source.type").String() != "base64" ||
				!isTextDocumentMediaType(block.Get("source.media_type").String()) {
				rebuiltBlocks = append(rebuiltBlocks, blockRaw)
				return true
			}
			decoded, err := base64.StdEncoding.DecodeString(block.Get("source.data").String())
			if err != nil || !utf8.Valid(decoded) {
				convErr = fmt.Errorf("invalid base64 UTF-8 text document")
				return false
			}
			for _, set := range []struct {
				path  string
				value string
			}{
				{"source.type", "text"},
				{"source.media_type", "text/plain"},
				{"source.data", string(decoded)},
			} {
				blockRaw, err = sjson.SetBytes(blockRaw, set.path, set.value)
				if err != nil {
					convErr = err
					return false
				}
			}
			rebuiltBlocks = append(rebuiltBlocks, blockRaw)
			messageChanged = true
			return true
		})
		if convErr != nil {
			return false
		}

		if messageChanged {
			updated, err := sjson.SetRawBytes(messageRaw, "content", rawJSONArrayOf(rebuiltBlocks))
			if err != nil {
				convErr = err
				return false
			}
			messageRaw = updated
			anyChanged = true
		}
		rebuiltMessages = append(rebuiltMessages, messageRaw)
		return true
	})
	if convErr != nil {
		return nil, convErr
	}
	if !anyChanged {
		return body, nil
	}

	updated, err := sjson.SetRawBytes(body, "messages", rawJSONArrayOf(rebuiltMessages))
	if err != nil {
		return nil, err
	}
	return updated, nil
}

func isTextDocumentMediaType(value string) bool {
	media, _, err := mime.ParseMediaType(value)
	if err != nil {
		return false
	}
	return strings.HasPrefix(media, "text/") || media == "application/json" || strings.HasPrefix(media, "application/") && strings.HasSuffix(media, "+json")
}
