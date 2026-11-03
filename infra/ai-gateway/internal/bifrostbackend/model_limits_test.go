package bifrostbackend

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	api "github.com/hansjone/auriga/infra/gen/aiapi"
)

func TestPublicModelSuppliesPoolCapabilitiesWithoutUpstreamIdentity(t *testing.T) {
	entry := publicModel(api.RelayPool{Id: "public-deepseek", ContextWindow: 32768, MaxOutputTokens: 4096})
	if entry["id"] != "public-deepseek" || entry["context_window"] != int64(32768) || entry["max_output_tokens"] != int64(4096) || entry["created"] != 0 || len(entry) != 6 {
		t.Fatalf("public catalog capability projection: %#v", entry)
	}
}

func TestPublicModelNeverInventsUnknownCapabilitiesOrClampsKnownValues(t *testing.T) {
	unknown := publicModel(api.RelayPool{Id: "auto"})
	if _, ok := unknown["context_window"]; ok {
		t.Fatal("unknown context invented")
	}
	if _, ok := unknown["max_output_tokens"]; ok {
		t.Fatal("unknown output invented")
	}
	known := publicModel(api.RelayPool{Id: "auto", ContextWindow: 2000000, MaxOutputTokens: 128000})
	if known["context_window"] != int64(2000000) || known["max_output_tokens"] != int64(128000) {
		t.Fatal("known capabilities clamped")
	}
	if _, ok := publicModel(api.RelayPool{Id: "legacy", ContextWindow: 1})["context_window"]; ok {
		t.Fatal("invalid legacy context poisoned the directory")
	}
}

func TestPoolCapabilitiesMatchCatalogConsumerLowerBound(t *testing.T) {
	for _, tc := range []struct {
		context, output int64
		valid           bool
	}{{0, 0, true}, {0, 1, true}, {1, 0, false}, {2, 0, true}, {2, 1, true}, {2, 2, false}, {-1, 0, false}, {maxInteger, 1, true}} {
		if got := validPoolCapabilities(tc.context, tc.output); got != tc.valid {
			t.Fatalf("capability %d/%d valid=%v", tc.context, tc.output, got)
		}
	}
	pool := api.RelayPool{Id: "auto", ContextWindow: 1, Routes: []api.RelayRoute{{ProviderId: "019a0000-0000-7000-8000-000000000001", Model: "fixture", Weight: 1}}, Price: api.Price{Currency: "USD", Version: "fixture", EffectiveAt: time.Now().Add(-time.Minute), InputMicrosPerMillionTokens: 1, OutputMicrosPerMillionTokens: 1}}
	if err := (&Backend{}).SavePool(t.Context(), "tenant", pool); err != invalid {
		t.Fatal("invalid context reached persistence")
	}
}

func TestDSHOutputIsIndependentOfAdvertisedCapability(t *testing.T) {
	for _, tc := range []struct {
		name, body, field string
		want              int64
	}{
		{"larger-than-old-capability", `{"max_tokens":32768}`, "", 32768},
		{"configured-rc3", `{"max_tokens":4096}`, "", 4096},
		{"completion-spelling", `{"max_completion_tokens":4096}`, "", 4096},
		{"absent-stays-absent", `{}`, "", 0},
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
				if w.Code != 400 || !strings.Contains(w.Body.String(), `"maximum":9007199254740991`) || !strings.Contains(w.Body.String(), field) {
					t.Fatal("rejection must identify safe field without a pool capacity ceiling")
				}
			}
		})
	}
}
