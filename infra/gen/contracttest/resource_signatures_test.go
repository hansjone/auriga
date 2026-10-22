// Package contracttest verifies cross-language contract fixtures, not production trust enforcement.
package contracttest

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/hansjone/auriga/infra/gen/aiapi"
	"github.com/hansjone/auriga/infra/gen/resourceapi"
)

func TestResourceSignatureGoldenVectors(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "contracts", "resource", "signature-vectors.json"))
	if err != nil {
		t.Fatal(err)
	}
	var input struct {
		Cases []struct {
			Name, Schema, Canonical, SHA256, PublicKeySpki string
			Document                                       map[string]any
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&input); err != nil {
		t.Fatal(err)
	}
	if len(input.Cases) != 4 {
		t.Fatalf("expected four shared vectors, got %d", len(input.Cases))
	}
	for _, v := range input.Cases {
		t.Run(v.Name, func(t *testing.T) {
			signature := v.Document["signature"].(map[string]any)["value"].(string)
			assertGeneratedResourceRoundtrip(t, v.Schema, v.Document)
			delete(v.Document, "signature")
			canonical, err := canonicalJSON(v.Document)
			if err != nil {
				t.Fatal(err)
			}
			if canonical != v.Canonical {
				t.Fatalf("canonical bytes mismatch: %s", canonical)
			}
			digest := sha256.Sum256([]byte(canonical))
			if hex.EncodeToString(digest[:]) != v.SHA256 {
				t.Fatal("canonical SHA-256 mismatch")
			}
			assertSignature(t, v.PublicKeySpki, signature, canonical)
		})
	}
}

func TestGeneratedRequiredNullableUsage(t *testing.T) {
	input := []byte(`{"requestId":"019a0000-0000-7000-8000-000000000001","accountId":"019a0000-0000-7000-8000-000000000001","modelId":"example-model","outcome":"unknown","usageQuality":"unknown","inputTokens":null,"outputTokens":null,"costMicros":null,"frozenMicros":1000,"frozenTokens":10,"currency":"USD","dayPeriodStart":"2026-10-21T00:00:00Z","monthPeriodStart":"2026-10-01T00:00:00Z","priceVersion":"v1","createdAt":"2026-10-21T08:00:00Z"}`)
	var usage aiapi.Usage
	if err := json.Unmarshal(input, &usage); err != nil {
		t.Fatal(err)
	}
	if !usage.InputTokens.IsSet() || usage.InputTokens.Get() != nil {
		t.Fatal("explicit null became missing or zero")
	}
	zero := int64(0)
	usage.InputTokens.Set(&zero)
	if usage.InputTokens.Get() == nil || *usage.InputTokens.Get() != 0 {
		t.Fatal("explicit zero was not preserved")
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(input, &body); err != nil {
		t.Fatal(err)
	}
	delete(body, "inputTokens")
	missing, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(missing, &usage); err == nil {
		t.Fatal("missing required nullable count accepted")
	}
	if err := json.Unmarshal([]byte(`{}`), &resourceapi.ResourceEnvelope{}); err == nil {
		t.Fatal("missing resource identity accepted")
	}
}

func assertGeneratedResourceRoundtrip(t *testing.T, schema string, document map[string]any) {
	t.Helper()
	data, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	var model any
	switch schema {
	case "ResourceEnvelope":
		model = &resourceapi.ResourceEnvelope{}
	case "RevocationList":
		model = &resourceapi.RevocationList{}
	case "DowngradeAuthorization":
		model = &resourceapi.DowngradeAuthorization{}
	default:
		t.Fatal("unknown vector schema")
	}
	if err := json.Unmarshal(data, model); err != nil {
		t.Fatal(err)
	}
	roundtrip, err := json.Marshal(model)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	decoder := json.NewDecoder(bytes.NewReader(roundtrip))
	decoder.UseNumber()
	if err := decoder.Decode(&decoded); err != nil {
		t.Fatal(err)
	}
	want, err := canonicalJSON(document)
	if err != nil {
		t.Fatal(err)
	}
	got, err := canonicalJSON(decoded)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("generated resource model changed signed JSON: got %s, want %s", got, want)
	}
}

func TestJavaSignedVectors(t *testing.T) {
	file := os.Getenv("AURIGA_JAVA_SIGNATURE_VECTORS")
	if file == "" {
		t.Skip("optional Java->Go transfer requires AURIGA_JAVA_SIGNATURE_VECTORS from the Java contract test")
	}
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var vectors []struct{ Name, Canonical, Signature, PublicKeySpki string }
	if err := json.Unmarshal(data, &vectors); err != nil {
		t.Fatal(err)
	}
	if len(vectors) != 4 {
		t.Fatal("Java must sign all four vectors")
	}
	for _, v := range vectors {
		t.Run(v.Name, func(t *testing.T) { assertSignature(t, v.PublicKeySpki, v.Signature, v.Canonical) })
	}
}

func TestJCSUTF16OrderAndEscaping(t *testing.T) {
	value := map[string]any{"\ue000": "line\n\t\x01/\\\"", "😀": "中文"}
	canonical, err := canonicalJSON(value)
	if err != nil {
		t.Fatal(err)
	}
	if canonical != "{\"😀\":\"中文\",\"\ue000\":\"line\\n\\t\\u0001/\\\\\\\"\"}" {
		t.Fatal(canonical)
	}
	if _, err := canonicalJSON(json.Number("9007199254740992")); err == nil {
		t.Fatal("unsafe integer accepted")
	}
	if _, err := canonicalJSON(json.Number("1.5")); err == nil {
		t.Fatal("fractional resource counter accepted")
	}
	if _, err := canonicalJSON(string([]byte{0xff})); err == nil {
		t.Fatal("invalid UTF-8 accepted")
	}
}

func assertSignature(t *testing.T, spki, signature, canonical string) {
	t.Helper()
	keyDER, err := base64.RawURLEncoding.Strict().DecodeString(spki)
	if err != nil {
		t.Fatal(err)
	}
	key, err := x509.ParsePKIXPublicKey(keyDER)
	if err != nil {
		t.Fatal(err)
	}
	publicKey, ok := key.(ed25519.PublicKey)
	if !ok {
		t.Fatal("expected Ed25519 public key")
	}
	sig, err := base64.RawURLEncoding.Strict().DecodeString(signature)
	if err != nil {
		t.Fatal(err)
	}
	if !ed25519.Verify(publicKey, []byte(canonical), sig) {
		t.Fatal("Ed25519 signature rejected")
	}
	if ed25519.Verify(publicKey, []byte(canonical+" "), sig) {
		t.Fatal("tampered canonical bytes accepted")
	}
}

// The signature schemas intentionally allow only safe integer numbers, no floating-point numbers.
func canonicalJSON(value any) (string, error) {
	switch v := value.(type) {
	case nil:
		return "null", nil
	case bool:
		return strconv.FormatBool(v), nil
	case json.Number:
		n, err := strconv.ParseUint(string(v), 10, 64)
		if err != nil || n > 9007199254740991 {
			return "", fmt.Errorf("unsafe resource integer %s", v)
		}
		return strconv.FormatUint(n, 10), nil
	case string:
		return quoteJCS(v)
	case []any:
		items := make([]string, len(v))
		for i, item := range v {
			s, err := canonicalJSON(item)
			if err != nil {
				return "", err
			}
			items[i] = s
		}
		return "[" + strings.Join(items, ",") + "]", nil
	case map[string]any:
		keys := make([]string, 0, len(v))
		for key := range v {
			keys = append(keys, key)
		}
		sort.Slice(keys, func(i, j int) bool {
			a, b := utf16.Encode([]rune(keys[i])), utf16.Encode([]rune(keys[j]))
			for k := 0; k < len(a) && k < len(b); k++ {
				if a[k] != b[k] {
					return a[k] < b[k]
				}
			}
			return len(a) < len(b)
		})
		items := make([]string, 0, len(keys))
		for _, key := range keys {
			quoted, err := quoteJCS(key)
			if err != nil {
				return "", err
			}
			body, err := canonicalJSON(v[key])
			if err != nil {
				return "", err
			}
			items = append(items, quoted+":"+body)
		}
		return "{" + strings.Join(items, ",") + "}", nil
	default:
		return "", fmt.Errorf("outside resource signature schema: %T", value)
	}
}

func quoteJCS(value string) (string, error) {
	if !utf8.ValidString(value) {
		return "", fmt.Errorf("invalid UTF-8")
	}
	var out strings.Builder
	out.WriteByte('"')
	for _, r := range value {
		switch r {
		case '"':
			out.WriteString(`\"`)
		case '\\':
			out.WriteString(`\\`)
		case '\b':
			out.WriteString(`\b`)
		case '\f':
			out.WriteString(`\f`)
		case '\n':
			out.WriteString(`\n`)
		case '\r':
			out.WriteString(`\r`)
		case '\t':
			out.WriteString(`\t`)
		default:
			if r < 0x20 {
				fmt.Fprintf(&out, `\u%04x`, r)
			} else {
				out.WriteRune(r)
			}
		}
	}
	out.WriteByte('"')
	return out.String(), nil
}
