package telemetry

import (
	"encoding/json"
	"io"
	"regexp"
	"strings"
)

const diagnosticSecretNames = `[a-z0-9_-]*(?:password|passwd|pwd|secret|credential|token|ticket|claim|private[_-]?key|api[_-]?key|cookie|totp)|authorization`

var diagnosticSecretKey = regexp.MustCompile(`(?i)^(?:` + diagnosticSecretNames + `)$`)
var diagnosticCredentialText = regexp.MustCompile(`(?i)\b(?:` + diagnosticSecretNames + `)\s*[:=]`)

// MaskDiagnosticValue sanitizes an untrusted STRING leaf before JSON projection.
// JSON encoded inside that string is decoded with a depth bound, so escaping
// cannot hide credential keys. It is not used to discard whole messages/errors.
func MaskDiagnosticValue(value string) string {
	return maskDiagnosticText(value, 0)
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
		for key, field := range typed {
			if diagnosticSecretKey.MatchString(key) {
				typed[key] = "****"
			} else {
				typed[key] = maskDiagnosticNode(field, depth+1)
			}
		}
	case []any:
		for index, field := range typed {
			typed[index] = maskDiagnosticNode(field, depth+1)
		}
	case string:
		return maskDiagnosticText(typed, depth)
	}
	return value
}
