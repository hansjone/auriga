package bifrost

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/hansjone/auriga/infra/ai-gateway/internal/sharedauth"
	"github.com/hansjone/auriga/infra/ai-gateway/internal/sharedmodel"
	api "github.com/hansjone/auriga/infra/gen/aiapi"
)

func TestSharedRelayMemberRoutingAndStreamUsage(t *testing.T) {
	var entropy [32]byte
	rand.Read(entropy[:])
	password := hex.EncodeToString(entropy[:])
	path := filepath.Join(t.TempDir(), "password")
	if err := os.WriteFile(path, []byte(password), 0600); err != nil {
		t.Fatal(err)
	}
	tenant, pool := uuid.NewString(), uuid.NewString()
	makeCall := func() sharedmodel.SharedCall {
		return sharedmodel.SharedCall{
			Binding: sharedauth.Binding{TenantID: tenant, MemberID: uuid.NewString(), PlacementID: uuid.NewString(), PhysicalInstanceID: pool, Mode: "shared", Epoch: 1, Revision: 1, SessionID: uuid.NewString(), PlatformSessionID: uuid.NewString(), AuthGeneration: 1},
			TaskID:  uuid.NewString(), CallID: uuid.NewString(), Body: []byte(`{"model":"public","messages":[{"role":"user","content":"hello"}],"stream":true,"user":"forged","metadata":{"account_id":"forged"}}`),
		}
	}
	calls := []sharedmodel.SharedCall{makeCall(), makeCall()}
	var mu sync.Mutex
	seen := map[string]int{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok || user != "auriga-member" || pass != password {
			t.Error("relay auth")
			w.WriteHeader(401)
			return
		}
		if r.URL.Path == "/internal/members/ensure" {
			var input api.RelayMemberEnsure
			if json.NewDecoder(r.Body).Decode(&input) != nil || input.TenantId != tenant {
				t.Error("member input")
				return
			}
			json.NewEncoder(w).Encode(api.RelayKeyIssued{Key: api.RelayKey{Id: input.AccountId, AccountId: input.AccountId, Name: memberName(tenant, input.AccountId), Source: "member", Active: true}, Secret: "sk-bf-" + input.AccountId})
			return
		}
		if r.URL.Path != "/v1/chat/completions" {
			t.Error(r.URL.Path)
			w.WriteHeader(404)
			return
		}
		member := strings.TrimPrefix(r.Header.Get("X-Bf-Vk"), "sk-bf-")
		var body struct {
			User     string            `json:"user"`
			Metadata map[string]string `json:"metadata"`
			Options  struct {
				Include bool `json:"include_usage"`
			} `json:"stream_options"`
		}
		if json.NewDecoder(r.Body).Decode(&body) != nil || body.User != tenant+":"+member || body.Metadata["account_id"] != member || !body.Options.Include {
			t.Error("forged/shared member identity")
		}
		if !strings.HasPrefix(r.Header.Get("Idempotency-Key"), "shared-") {
			t.Error("missing call dedup key")
		}
		mu.Lock()
		seen[member]++
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\ndata: {\"choices\":[],\"usage\":{\"prompt_tokens\":2,\"completion_tokens\":3,\"total_tokens\":5}}\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()
	relay, err := NewSharedRelay(server.URL, path, MemberPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	defer relay.Close()
	var wg sync.WaitGroup
	for _, call := range calls {
		wg.Add(1)
		go func() {
			defer wg.Done()
			chunks := 0
			usage, err := relay.Stream(context.Background(), call, func([]byte) error { chunks++; return nil })
			if err != nil || !usage.Known || usage.InputTokens != 2 || usage.OutputTokens != 3 || chunks != 2 {
				t.Errorf("usage=%+v chunks=%d err=%v", usage, chunks, err)
			}
		}()
	}
	wg.Wait()
	if seen[calls[0].Binding.MemberID] != 1 || seen[calls[1].Binding.MemberID] != 1 {
		t.Fatal(seen)
	}
	mutated := calls[0]
	mutated.Binding.Epoch++
	if sharedRelayKey(mutated) == sharedRelayKey(calls[0]) {
		t.Fatal("epoch omitted from key")
	}
}

func TestSharedRelayRejectsRoutingAndInvalidUsage(t *testing.T) {
	for _, raw := range []string{`{"error":{"message":"private material"}}`, `{"usage":{"prompt_tokens":2,"completion_tokens":-1,"total_tokens":1}}`, `{"usage":{"prompt_tokens":2,"completion_tokens":3,"total_tokens":4}}`} {
		var usage sharedmodel.SharedUsage
		if captureSharedUsage([]byte(raw), &usage) == nil {
			t.Fatal(raw)
		}
	}
}
