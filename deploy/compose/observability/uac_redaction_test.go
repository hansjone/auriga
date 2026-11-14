package observability_test

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// Alloy stage.replace replaces each capture group, not the whole regex match.
func redact(line string, expressions []*regexp.Regexp) string {
	for _, expression := range expressions {
		var output strings.Builder
		cursor := 0
		for _, match := range expression.FindAllStringSubmatchIndex(line, -1) {
			output.WriteString(line[cursor:match[2]])
			output.WriteString("[REDACTED]")
			cursor = match[3]
		}
		output.WriteString(line[cursor:])
		line = output.String()
	}
	return line
}

func TestUacCollectionRedactsJSONAndEscapedJSONWithoutCorruptingSafeMetadata(t *testing.T) {
	_, source, _, _ := runtime.Caller(0)
	base := filepath.Dir(source)
	paths := []string{filepath.Join(base, "config.alloy"), filepath.Join(base, "..", "..", "nas", "observability", "config.alloy")}
	var entropy [24]byte
	if _, err := rand.Read(entropy[:]); err != nil {
		t.Fatal(err)
	}
	secret := hex.EncodeToString(entropy[:]) + "\"\\escaped"
	for _, path := range paths {
		t.Run(filepath.Base(filepath.Dir(filepath.Dir(path))), func(t *testing.T) {
			bytes, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			config := string(bytes)
			if !strings.Contains(config, `forward_to = [loki.process.uac_safe.receiver]`) || !strings.Contains(config, `loki.process "uac_safe"`) {
				t.Fatal("missing collector redaction")
			}
			matches := regexp.MustCompile(`expression\s*=\s*("(?:\\.|[^"\\])*")`).FindAllStringSubmatch(config, -1)
			if len(matches) != 2 {
				t.Fatalf("expected two bounded JSON redactors, got %d", len(matches))
			}
			var expressions []*regexp.Regexp
			for _, match := range matches {
				expression, err := strconv.Unquote(match[1])
				if err != nil {
					t.Fatal(err)
				}
				compiled, err := regexp.Compile(expression)
				if err != nil {
					t.Fatal(err)
				}
				if compiled.NumSubexp() != 1 {
					t.Fatal("only the value may be captured")
				}
				expressions = append(expressions, compiled)
			}
			inner := map[string]any{"stage": "token_verify", "phase": "end", "correlationId": "00000000-0000-7000-8000-000000000001", "tokenHashPrefix": hex.EncodeToString(entropy[:6]), "tokenMissing": false, "cookiePresent": true, "empNo": "001Ab", "bo": map[string]any{"code": "0000", "Token": secret, "department": secret, "email": secret, "name": secret, "verifyCode": secret, "x": secret, "y": secret}, "headers": map[string]any{"Authorization": "Bearer " + secret, "Set-Cookie": secret}}
			encoded, err := json.Marshal(inner)
			if err != nil {
				t.Fatal(err)
			}
			outer, err := json.Marshal(map[string]any{"logger": "auriga.iam.uac.detail", "message": string(encoded), "secret": secret})
			if err != nil {
				t.Fatal(err)
			}
			result := redact(string(outer), expressions)
			if strings.Contains(result, hex.EncodeToString(entropy[:])) {
				t.Fatal("collector retained a secret")
			}
			var envelope map[string]any
			if err = json.Unmarshal([]byte(result), &envelope); err != nil {
				t.Fatalf("collector corrupted wrapper: %v", err)
			}
			var event map[string]any
			if err = json.Unmarshal([]byte(envelope["message"].(string)), &event); err != nil {
				t.Fatalf("collector corrupted event: %v", err)
			}
			if event["correlationId"] != inner["correlationId"] || event["tokenHashPrefix"] != inner["tokenHashPrefix"] || event["empNo"] != "001Ab" || event["tokenMissing"] != false || event["cookiePresent"] != true {
				t.Fatal("collector changed safe correlation/identity metadata")
			}
			if event["bo"].(map[string]any)["code"] != "0000" {
				t.Fatal("business code changed")
			}
		})
	}
}
