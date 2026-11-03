package bifrostbackend

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
)

func TestRunnerDoesNotInventOrIncreaseAnOutputLimit(t *testing.T) {
	for _, limit := range []int64{0, 1, 65536} {
		t.Run(strconv.FormatInt(limit, 10), func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body map[string]json.RawMessage
				if json.NewDecoder(r.Body).Decode(&body) != nil {
					t.Error("bad body")
				}
				var actual int64
				for _, key := range []string{"max_tokens", "max_completion_tokens"} {
					if value := body[key]; value != nil {
						if json.Unmarshal(value, &actual) != nil {
							t.Error("invalid output limit")
						}
					}
				}
				if actual != limit {
					t.Errorf("wire limit=%d request=%d", actual, limit)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(reviewReply))
			}))
			defer upstream.Close()
			request := map[string]any{"model": "auto", "messages": []map[string]string{{"role": "user", "content": "hello"}}}
			if limit > 0 {
				request["max_completion_tokens"] = limit
			}
			raw, _ := json.Marshal(request)
			_, err := Run(t.Context(), []Route{{ID: "provider", Kind: "openai", URL: upstream.URL, Secret: "fixture", Model: "upstream", Weight: 1}}, raw, true, func([]byte) error { return nil })
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}
