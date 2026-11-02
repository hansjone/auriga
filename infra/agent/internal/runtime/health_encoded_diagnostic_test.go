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
	for name, version := range map[string]string{
		"object":           `{"password":"` + sentinel + `","note":"public-note"}`,
		"nested-string":    string(nested),
		"wrapped-string":   string(wrapped),
		"escaped-key":      `{"\u0070assword":"` + sentinel + `","note":"public-note"}`,
		"credential-alias": `{"privateKey":"` + sentinel + `","note":"public-note"}`,
		"opaque-escaped":   `version={\"password\":\"` + sentinel + ` with spaces\"}`,
		"quoted-secret":    `{"password":"prefix \"` + sentinel + `\" suffix","note":"public-note"}`,
		"plain-version":    "fixture.1-public-note",
		"malformed-object": `{"password":"prefix ` + sentinel,
		"mixed-object":     `version {"password":"prefix ` + sentinel + ` suffix"}`,
		"plain-credential": `version password=prefix ` + sentinel + ` suffix`,
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
						t.Fatal("failure classification lost", event)
					}
				}
				if summaries != 1 {
					t.Fatalf("summary count=%d", summaries)
				}
			})
		}
	}
}
