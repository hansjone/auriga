package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/hansjone/auriga/infra/agent/internal/telemetry"
)

func TestHealthEncodedCredentialsNeverReachFinalJSON(t *testing.T) {
	const sentinel = "R2_SENTINEL_NOT_A_REAL_CREDENTIAL"
	const parent = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	nested, err := json.Marshal(map[string]any{"wrapper": `{"password":"` + sentinel + `","note":"nested-public-note"}`, "note": "outer-public-note"})
	if err != nil {
		t.Fatal(err)
	}
	wrapped, err := json.Marshal(string(nested))
	if err != nil {
		t.Fatal(err)
	}
	encode := func(value any) string {
		t.Helper()
		encoded, encodeErr := json.Marshal(value)
		if encodeErr != nil {
			t.Fatal(encodeErr)
		}
		return string(encoded)
	}
	encodedKey := `{"password":"` + sentinel + `"}`
	deepKey := any(map[string]any{"password": sentinel})
	for range 12 {
		deepKey = map[string]any{"layer": deepKey}
	}
	wide := make(map[string]any)
	for index := range 220 {
		wide[strconv.Itoa(index)] = "public-note"
	}
	wideValue := any(wide)
	for range 4 {
		wideValue = map[string]any{"layer": wideValue}
	}
	for name, version := range map[string]string{
		"object":                  `{"password":"` + sentinel + `","note":"public-note"}`,
		"nested-string":           string(nested),
		"wrapped-string":          string(wrapped),
		"escaped-key":             `{"\u0070assword":"` + sentinel + `","note":"public-note"}`,
		"credential-alias":        `{"privateKey":"` + sentinel + `","note":"public-note"}`,
		"opaque-escaped":          `version={\"password\":\"` + sentinel + ` with spaces\"}`,
		"quoted-secret":           `{"password":"prefix \"` + sentinel + `\" suffix","note":"public-note"}`,
		"plain-version":           "fixture.1-public-note",
		"malformed-object":        `{"password":"prefix ` + sentinel,
		"mixed-object":            `version {"password":"prefix ` + sentinel + ` suffix"}`,
		"plain-credential":        `version password=prefix ` + sentinel + ` suffix`,
		"encoded-key":             encode(map[string]string{encodedKey: "public-note"}),
		"encoded-credential-name": encode(map[string]string{encode(encode("password")): sentinel, "note": "public-note"}),
		"encoded-benign-name":     encode(map[string]string{encode(encode("note")): "public-note"}),
		"nested-key":              encode(map[string]any{"layer": map[string]string{encodedKey: "nested-public-note"}, "note": "outer-public-note"}),
		"wrapped-key":             encode(map[string]string{encode(encodedKey): "public-note"}),
		"unicode-key":             encode(map[string]string{`{"\u0070assword":"` + sentinel + `"}`: "public-note"}),
		"malformed-key":           encode(map[string]string{`prefix {\"password\":\"` + sentinel: "public-note"}),
		"credential-text-key":     encode(map[string]string{"password=prefix " + sentinel + " suffix": "public-note"}),
		"colliding-keys":          encode(map[string]string{encodedKey: "public-note-a", `{"password":"` + sentinel + `_2"}`: "public-note-b"}),
		"benign-key":              encode(map[string]string{`{"note":"key-public-note"}`: "public-note"}),
		"deep-key":                encode(map[string]string{encode(deepKey): "public-note"}),
		"wide-structure":          encode(wideValue),
		"large-plain":             strings.Repeat("v", 6000) + "public-note",
	} {
		for _, ready := range []bool{true, false} {
			path := "failure"
			if ready {
				path = "success"
			}
			t.Run(name+"/"+path, func(t *testing.T) {
				body, encodeErr := json.Marshal(map[string]any{"status": "ok", "ready": ready, "protocol": 1, "pluginVersion": version})
				if encodeErr != nil {
					t.Fatal(encodeErr)
				}
				if len(body) > 8*1024 {
					t.Fatal("fixture must exercise the accepted health response budget")
				}
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(body) }))
				defer server.Close()
				var logs bytes.Buffer
				ctx := telemetry.Start(context.Background(), parent, "instance-r2", 8, slog.New(slog.NewJSONHandler(&logs, nil)))
				budget := 200 * time.Millisecond
				if ready {
					budget = 2 * time.Second
				}
				ctx, cancel := context.WithTimeout(ctx, budget)
				defer cancel()
				err := waitInstanceHealth(ctx, server.URL)
				if ready && err != nil {
					t.Fatal("readiness changed", err)
				}
				if !ready && !errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("deadline identity changed: %v", err)
				}
				if err != nil && strings.Contains(err.Error(), sentinel) {
					t.Fatal("exception contains encoded credential")
				}
				if strings.Contains(logs.String(), sentinel) {
					t.Fatal("final JSON contains encoded credential", logs.String())
				}
				decoder := json.NewDecoder(bytes.NewReader(logs.Bytes()))
				summaries := 0
				for {
					var event map[string]any
					decodeErr := decoder.Decode(&event)
					if decodeErr == io.EOF {
						break
					}
					if decodeErr != nil {
						t.Fatal(decodeErr)
					}
					if event["msg"] != "instance health ready" && event["msg"] != "instance health wait failed" {
						continue
					}
					summaries++
					if event["trace_id"] != parent[3:35] || event["instance_id"] != "instance-r2" || event["attempts"].(float64) < 1 {
						t.Fatal("diagnostic context lost", event)
					}
					response := event["health_response"].(string)
					if !strings.Contains(response, `"status":"ok"`) || !strings.Contains(response, `"protocol":1`) {
						t.Fatal("public response diagnostics lost", event)
					}
					var projection struct {
						PluginVersion string `json:"pluginVersion"`
					}
					if err := json.Unmarshal([]byte(response), &projection); err != nil {
						t.Fatal("response projection lost", err)
					}
					if len(projection.PluginVersion) > 8*1024 {
						t.Fatal("sanitized version output exceeded its bound")
					}
					if name == "plain-version" && !strings.Contains(response, version) {
						t.Fatal("nonsecret version lost", event)
					}
					if name == "object" && !strings.Contains(response, "public-note") {
						t.Fatal("nonsecret nested diagnostic lost", event)
					}
					if (name == "nested-string" || name == "wrapped-string") && (!strings.Contains(response, "outer-public-note") || !strings.Contains(response, "nested-public-note")) {
						t.Fatal("nonsecret encoded diagnostics lost", event)
					}
					if !ready && (event["category"] != "not_ready" || event["http_status"] != float64(200) || !strings.Contains(event["error"].(string), "ready=false")) {
						// Existing failure identity/classification remains part of the assertion.
						t.Fatal("failure classification lost", event)
					}
					if strings.HasSuffix(name, "key") && !strings.Contains(response, "public-note") {
						t.Fatal("nonsecret key/value diagnostic lost", event)
					}
					if name == "colliding-keys" && (!strings.Contains(response, "public-note-a") || !strings.Contains(response, "public-note-b")) {
						t.Fatal("redacted key collision discarded a diagnostic", event)
					}
					if name == "benign-key" && !strings.Contains(response, "key-public-note") {
						t.Fatal("nonsecret encoded key lost", event)
					}
					if name == "large-plain" && projection.PluginVersion != version {
						t.Fatal("bounded nonsecret version changed")
					}
					if name == "deep-key" && !strings.Contains(response, "nesting limit") {
						t.Fatal("key nesting was not bounded")
					}
					if name == "wide-structure" && !strings.Contains(response, "size limit") {
						t.Fatal("diagnostic expansion was not bounded")
					}
				}
				if summaries != 1 {
					t.Fatalf("summary count=%d", summaries)
				}
			})
		}
	}
}
