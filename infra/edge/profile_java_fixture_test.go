package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// A short-lived real Edge/mTLS Node fixture, launched by the W cross-language IT.
func TestWorkspaceProfileHTTPFixture(t *testing.T) {
	dir := os.Getenv("AURIGA_PROFILE_FIXTURE_DIR")
	if dir == "" {
		t.Skip("launched only by SharedDshEdgeProfileIT")
	}
	der, err := os.ReadFile(filepath.Join(dir, "public-key.der"))
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := x509.ParsePKIXPublicKey(der)
	if err != nil {
		t.Fatal(err)
	}
	public, ok := parsed.(ed25519.PublicKey)
	if !ok {
		t.Fatal("fixture requires Ed25519")
	}
	tenant, pool := os.Getenv("AURIGA_PROFILE_TENANT"), os.Getenv("AURIGA_PROFILE_POOL")
	if !sharedUUID.MatchString(tenant) || !sharedUUID.MatchString(pool) {
		t.Fatal("invalid fixture binding")
	}
	var count atomic.Int32
	host := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/i/"+pool)
		mode := profileMode(path)
		if mode == "" || r.Method != "GET" || r.TLS == nil || len(r.TLS.VerifiedChains) == 0 || r.Header.Get(sharedProfileHeader) != "" || r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" || r.Header.Get("Auriga-Identity") != "" {
			t.Error("profile escaped its fixed mTLS Node hop")
			w.WriteHeader(503)
			return
		}
		control, err := os.ReadFile(filepath.Join(dir, "control.json"))
		if err != nil {
			t.Error(err)
			w.WriteHeader(503)
			return
		}
		var state struct {
			Failure string `json:"failure"`
		}
		if err := json.Unmarshal(control, &state); err != nil {
			t.Error(err)
			w.WriteHeader(503)
			return
		}
		body := runtimeProfileBody(mode, pool)
		t.Logf("profile Host request mode=%s failure=%s", mode, state.Failure)
		switch state.Failure {
		case "other-profile":
			other := "shared"
			if mode == other {
				other = "dedicated"
			}
			body = runtimeProfileBody(other, pool)
		case "extra":
			body["capabilities"].(map[string]any)["unknown"] = false
		case "cookie":
			w.Header().Set("Set-Cookie", "unexpected=1")
		case "redirect":
			w.Header().Set("Location", "/owner")
		case "oversize":
			body["padding"] = strings.Repeat("x", 16385)
		case "wrong-pool":
			body["physicalInstanceId"] = sharedJTI()
		}
		if err := os.WriteFile(filepath.Join(dir, "requests"), []byte(strconv.Itoa(int(count.Add(1)))), 0600); err != nil {
			t.Error(err)
		}
		w.Header().Set("Content-Type", "application/json")
		if state.Failure == "unavailable" {
			w.WriteHeader(503)
		}
		data, _ := sharedMarshal(body)
		_, _ = w.Write(data)
	})
	var nodeURL string
	g, _, _ := accessFixtureConfigured(t, false, http.NotFoundHandler(), func(cfg *Config, document map[string]any) {
		document["tenantId"] = tenant
		nodeURL = testNode(t, cfg, host, testNodeID).URL
	})
	t.Cleanup(g.Ingress.Close)
	_, identity, _ := ed25519.GenerateKey(rand.Reader)
	g.Shared = &SharedEntry{Authority: &sharedTestAuthority{active: false}, Store: &sharedTestStore{used: map[string]bool{}, cookies: map[string]SharedBrowserBinding{}}, TicketKeys: map[string]ed25519.PublicKey{"cross-language": public}, IdentityKey: identity, IdentityKid: "fixture"}
	g.Routes.(*MemoryRouteStore).Put(&InstanceRoute{InstanceID: pool, TenantID: tenant, NodeID: testNodeID, Ingress: nodeURL, State: "running", UpdatedAt: time.Now()})
	ready, err := json.Marshal(map[string]string{"portalUrl": g.Cfg.Access.PortalURL()})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ready-next.json"), ready, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(dir, "ready-next.json"), filepath.Join(dir, "ready.json")); err != nil {
		t.Fatal(err)
	}
	deadline := time.NewTimer(45 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(20 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-tick.C:
			if _, err := os.Stat(filepath.Join(dir, "stop")); err == nil {
				return
			}
		case <-deadline.C:
			t.Fatal("W profile integration did not stop its Edge fixture")
		}
	}
}
