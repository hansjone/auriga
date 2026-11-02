package telemetry

import (
	"encoding/json"
	"io"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

const diagnosticSecretNames = `[a-z0-9_-]*(?:password|passwd|pwd|secret|credential|token|ticket|claim|private[_-]?key|api[_-]?key|cookie|totp)|authorization`

const maxDiagnosticValueOutput = 8 * 1024

var diagnosticSecretKey = regexp.MustCompile(`(?i)^(?:` + diagnosticSecretNames + `)$`)
var diagnosticCredentialText = regexp.MustCompile(`(?i)\b(?:` + diagnosticSecretNames + `)\s*[:=]`)

// MaskDiagnosticValue sanitizes an untrusted STRING leaf before JSON projection.
// JSON encoded inside that string is decoded with a depth bound, so escaping
// cannot hide credential keys. It is not used to discard whole messages/errors.
func MaskDiagnosticValue(value string) string {
	masked := maskDiagnosticText(value, 0)
	// Key renaming/depth markers can expand an otherwise small health string.
	// Bound only this diagnostic leaf, preserving the surrounding public fields.
	if len(masked) > maxDiagnosticValueOutput {
		return "[diagnostic omitted: size limit]"
	}
	return masked
}

func maskDiagnosticText(value string, depth int) string {
	if len(value) > 65536 {
		return "[diagnostic omitted: size limit]"
	}
	if depth >= 6 {
		return "[diagnostic omitted: nesting limit]"
	}
	decoder := json.NewDecoder(strings.NewReader(value))
	decoder.UseNumber()
	var parsed any
	if decoder.Decode(&parsed) == nil && decoder.Decode(new(any)) == io.EOF {
		masked, err := json.Marshal(maskDiagnosticNode(parsed, depth+1))
		if err == nil {
			return string(masked)
		}
	}
	// Opaque structured text is not a complete JSON value. Do not guess its quoting
	// grammar or copy a credential fragment (including unterminated values) into JSON.
	// Only this leaf is omitted; status, readiness and exception diagnostics remain.
	if strings.ContainsAny(value, "\\{}[]\"'") {
		return "[diagnostic omitted: ambiguous encoded string]"
	}
	masked := Mask(value)
	if masked != value || diagnosticCredentialText.MatchString(value) {
		return "[diagnostic redacted: credential text]"
	}
	return value
}

func maskDiagnosticNode(value any, depth int) any {
	if depth >= 6 {
		return "[diagnostic omitted: nesting limit]"
	}
	switch typed := value.(type) {
	case map[string]any:
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		masked := make(map[string]any, len(typed))
		collision := 0
		for _, key := range keys {
			field := typed[key]
			safeKey := maskDiagnosticText(key, depth+1)
			candidate := safeKey
			for {
				if _, exists := masked[candidate]; !exists {
					break
				}
				collision++
				candidate = safeKey + " [redacted key " + strconv.Itoa(collision) + "]"
			}
			if isDiagnosticSecretKey(key, depth+1) {
				masked[candidate] = "****"
			} else {
				masked[candidate] = maskDiagnosticNode(field, depth+1)
			}
		}
		return masked
	case []any:
		for index, field := range typed {
			typed[index] = maskDiagnosticNode(field, depth+1)
		}
	case string:
		return maskDiagnosticText(typed, depth)
	}
	return value
}

// A JSON string used as a key can encode a credential NAME as well as contain
// an encoded object. Keep the same depth budget when classifying those names.
func isDiagnosticSecretKey(key string, depth int) bool {
	for depth < 6 {
		if diagnosticSecretKey.MatchString(key) {
			return true
		}
		var decoded string
		if json.Unmarshal([]byte(key), &decoded) != nil {
			return false
		}
		key = decoded
		depth++
	}
	return true // Ambiguous encoded names at the limit cannot expose their values.
}
