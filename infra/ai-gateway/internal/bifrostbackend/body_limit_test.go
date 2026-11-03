package bifrostbackend

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hansjone/auriga/infra/ai-gateway/internal/relaylimits"
)

func TestChatBodyBoundIsSeparateFromManagementAndModelCapacity(t *testing.T) {
	large := `{"messages":[{"role":"user","content":"` + strings.Repeat("a", 2<<20) + `"}]}`
	for _, tc := range []struct {
		path string
		want bool
	}{{"/v1/chat/completions", true}, {"/api/pools", false}} {
		r := httptest.NewRequest("POST", tc.path, strings.NewReader(large))
		var fields map[string]json.RawMessage
		err := decode(httptest.NewRecorder(), r, &fields)
		if (err == nil) != tc.want {
			t.Fatalf("path=%s err=%v", tc.path, err)
		}
	}
	r := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"content":"`+strings.Repeat("a", int(relaylimits.ChatBodyBytes))+`"}`))
	var fields map[string]json.RawMessage
	if err := decode(httptest.NewRecorder(), r, &fields); err != invalid {
		t.Fatal("transport body bound removed")
	}
}
