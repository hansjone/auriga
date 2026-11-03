package bifrostbackend

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	api "github.com/hansjone/auriga/infra/gen/aiapi"
)

func TestPublicModelSuppliesPoolCapabilitiesWithoutUpstreamIdentity(t *testing.T) {
	entry := publicModel(api.RelayPool{Id: "public-deepseek", ContextWindow: 32768, MaxOutputTokens: 4096})
	if entry["id"] != "public-deepseek" || entry["context_window"] != int64(32768) || entry["max_output_tokens"] != int64(4096) || len(entry) != 5 {
		t.Fatalf("public catalog capability projection: %#v", entry)
	}
}

func TestDSHDefaultOutputMustUseAdvertisedLimit(t *testing.T) {
	// rc.3's unspecified model capability defaults to 32768. The configured
	// pool remains at 4096; publishing 4096 fixes the client without widening it.
	for _, tc := range []struct {
		name, body, field string
		want              int64
	}{
		{"unconfigured-rc3", `{"max_tokens":32768}`, "/max_tokens", 0},
		{"configured-rc3", `{"max_tokens":4096}`, "", 4096},
		{"completion-spelling", `{"max_completion_tokens":4096}`, "", 4096},
		{"absent", `{}`, "", 4096},
		{"both-agree", `{"max_tokens":4096,"max_completion_tokens":4096}`, "", 4096},
		{"both-conflict", `{"max_tokens":4096,"max_completion_tokens":64}`, "/max_completion_tokens", 0},
		{"zero", `{"max_tokens":0}`, "/max_tokens", 0},
		{"fraction", `{"max_tokens":1.5}`, "/max_tokens", 0},
		{"null", `{"max_tokens":null}`, "/max_tokens", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var fields map[string]json.RawMessage
			if err := json.Unmarshal([]byte(tc.body), &fields); err != nil {
				t.Fatal(err)
			}
			limit, field, err := outputTokenLimit(fields, 4096)
			if limit != tc.want || field != tc.field || (err != nil) != (tc.field != "") {
				t.Fatalf("limit=%d field=%q err=%v", limit, field, err)
			}
			if err != nil {
				w := httptest.NewRecorder()
				rejectOutputTokenLimit(w, field, 4096)
				if w.Code != 400 || !strings.Contains(w.Body.String(), `"maximum":4096`) || !strings.Contains(w.Body.String(), field) {
					t.Fatal("rejection must identify safe field and public maximum")
				}
			}
		})
	}
}
