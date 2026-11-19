package ingress

import (
	"bufio"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	bolt "go.etcd.io/bbolt"
)

func TestSharedBrowserNDJSONStopsOnCurrentPlacementRevocation(t *testing.T) {
	public, private, _ := ed25519.GenerateKey(rand.Reader)
	now := time.Now()
	claims := sharedTestClaims(true, now.Unix())
	claims["method"], claims["canonicalPath"] = "POST", "/auriga/shared/browser/stream"
	a := &sharedNodeAuthority{binding: claims["binding"].(map[string]any), active: true}
	closed := make(chan struct{})
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/x-ndjson")
		_, _ = io.WriteString(w, "{\"type\":\"item\",\"value\":\"synthetic\"}\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		close(closed)
	}))
	defer up.Close()
	target, _ := url.Parse(up.URL)
	db, err := bolt.Open(filepath.Join(t.TempDir(), "replay.db"), 0600, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	s := New(Config{Shared: &SharedIngress{Keys: map[string]ed25519.PublicKey{"test": public}, Authority: a, Replay: &SharedIdentityReplayStore{DB: db}}, Registry: NewMapRegistry(&Instance{ID: sharedTestID, Shared: true, Upstream: target}), Now: func() time.Time { return now }})
	node := httptest.NewServer(http.HandlerFunc(s.route))
	defer node.Close()
	req, err := http.NewRequest("POST", node.URL+"/i/"+sharedTestID+"/auriga/shared/browser/stream", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set(identityHeader, sharedTestSign(claims, private, true))
	client := &http.Client{Timeout: 3 * time.Second}
	response, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	reader := bufio.NewReader(response.Body)
	line, err := reader.ReadString('\n')
	if err != nil || response.StatusCode != 200 || line != "{\"type\":\"item\",\"value\":\"synthetic\"}\n" {
		t.Fatal("original stream not forwarded", line, err)
	}
	a.mu.Lock()
	a.active = false
	cancels := append([]context.CancelFunc(nil), a.cancels...)
	a.mu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("placement revoke did not cancel actual upstream stream")
	}
	if rest, _ := io.ReadAll(reader); len(rest) != 0 {
		t.Fatal("late native stream data returned after revoke")
	}
}

func TestSharedBrowserForwardsNativeHTTPAndFencesOwnerChannels(t *testing.T) {
	for _, mode := range []string{"shared", "dedicated"} {
		t.Run(mode, func(t *testing.T) {
			public, private, _ := ed25519.GenerateKey(rand.Reader)
			now := time.Now()
			claims := sharedTestClaims(true, now.Unix())
			binding := claims["binding"].(map[string]any)
			binding["mode"] = mode
			a := &sharedNodeAuthority{binding: binding, active: true}
			calls := 0
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Header.Get(identityHeader) == "" {
					t.Error("identity was not preserved")
				}
				if r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" {
					t.Error("managed mode borrowed an owner credential")
				}
				if r.Method == "POST" {
					body, _ := io.ReadAll(r.Body)
					if string(body) != `{"fixture":true}` {
						t.Error("request body rewritten")
					}
				}
				w.WriteHeader(http.StatusNoContent)
			}))
			defer up.Close()
			target, _ := url.Parse(up.URL)
			db, err := bolt.Open(filepath.Join(t.TempDir(), "replay.db"), 0600, nil)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := db.Close(); err != nil {
					t.Error(err)
				}
			})
			s := New(Config{Shared: &SharedIngress{Keys: map[string]ed25519.PublicKey{"test": public}, Authority: a, Replay: &SharedIdentityReplayStore{DB: db}}, Registry: NewMapRegistry(&Instance{ID: sharedTestID, Shared: true, Upstream: target}), Now: func() time.Time { return now }})
			sequence := 0
			request := func(path, method string, upgrade bool) int {
				sequence++
				claims["jti"] = fmt.Sprintf("00000000-0000-4000-8000-%012d", sequence)
				claims["canonicalPath"], claims["method"] = path, method
				r := httptest.NewRequest(method, "http://node/i/"+sharedTestID+path, strings.NewReader(`{"fixture":true}`))
				r.Header.Set(identityHeader, sharedTestSign(claims, private, true))
				r.Header.Set("Cookie", "fixture_owner=must-not-authorize-shared")
				r.Header.Set("Authorization", "Bearer fixture-owner")
				if upgrade {
					r.Header.Set("Connection", "keep-alive, Upgrade")
					r.Header.Set("Upgrade", "websocket")
				}
				w := httptest.NewRecorder()
				s.route(w, r)
				return w.Code
			}
			for _, route := range []struct{ path, method string }{{"/", "GET"}, {"/index.html", "GET"}, {"/assets/native.js", "GET"}, {"/auriga/shared/browser/rpc", "POST"}, {"/auriga/shared/browser/stream", "POST"}, {"/auriga/shared/messages", "POST"}} {
				if mode == "dedicated" {
					if route.path == "/auriga/shared/messages" {
						continue
					}
					route.path = strings.Replace(route.path, "/auriga/shared/", "/auriga/dedicated/", 1)
				}
				if status := request(route.path, route.method, false); status != 204 {
					t.Fatal("approved HTTP not forwarded", route, status)
				}
			}
			before := calls
			for _, path := range []string{"/api", "/api/remote.mux", "/connect", "/connect/", "/%61pi/remote.mux", "/c%6fnnect"} {
				status := request(path, "GET", false)
				if status != 403 {
					t.Fatal("managed mode reached native owner channel", path, status)
				}
			}
			if calls != before {
				t.Fatal("rejected shared owner route reached upstream")
			}
			if status := request("/api/remote.mux", "GET", true); status != 403 {
				t.Fatal("owner websocket reached shared Host", status)
			}
			if status := request("/auriga/"+mode+"/browser/stream", "GET", true); status != 403 {
				t.Fatal("shared arbitrary upgrade accepted", status)
			}
			a.mu.Lock()
			a.active = false
			a.mu.Unlock()
			before = calls
			if status := request("/auriga/shared/messages", "POST", false); status == 204 || calls != before {
				t.Fatal("revoked placement reached message settings", status)
			}
		})
	}
}
