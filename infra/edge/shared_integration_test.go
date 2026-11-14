package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestSharedNATSAtomicBinding(t *testing.T) {
	pass, _ := randomID(24)
	endpoint, stop := startTestNATS(t, "shared", pass)
	defer stop()
	nc, err := nats.Connect(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()
	js, err := jetstream.New(nc)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	kv, err := js.CreateKeyValue(ctx, jetstream.KeyValueConfig{Bucket: "shared-entry", TTL: 12 * time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	store := &NATSSharedEntryStore{KV: kv}
	c := sharedTestClaims(false, time.Now().Unix())
	raw, _ := json.Marshal(c)
	c, _ = sharedDecode(raw)
	binding := SharedBrowserBinding{Binding: c["binding"].(map[string]any), PlatformSessionID: sharedTestID, ExpiresAt: time.Now().Add(time.Hour)}
	jti := sharedString(c["jti"])
	cookie := jti + ".opaque"
	if err = store.ConsumeAndBind(ctx, jti, time.Now().Add(time.Minute), cookie, binding); err != nil {
		t.Fatal(err)
	}
	// A fresh store handle sees both binding and consumed ticket.
	store = &NATSSharedEntryStore{KV: kv}
	b, err := store.Lookup(ctx, cookie)
	if err != nil || !sharedEqual(b.Binding, binding.Binding) {
		t.Fatal("lost binding", err)
	}
	if err = store.ConsumeAndBind(ctx, jti, time.Now().Add(time.Minute), cookie, binding); err == nil || err.Error() != "common.conflict" {
		t.Fatal("replay", err)
	}
	if _, err = store.Lookup(ctx, jti+".forged"); err == nil {
		t.Fatal("forged cookie")
	}
}
func TestSharedIPBrowser(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	seen := make(chan map[string]any, 4)
	browser := newIPBrowser(t, false, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, e := sharedVerify(r.Header.Get("Auriga-Identity"), map[string]ed25519.PublicKey{"test": pub}, true, time.Now().Unix())
		if e != nil {
			t.Error(e)
		}
		seen <- c
		w.WriteHeader(204)
	}))
	now := time.Now()
	c := sharedTestClaims(false, now.Unix())
	c["binding"].(map[string]any)["tenantId"] = browser.g.Cfg.Access.TenantID()
	raw, _ := json.Marshal(c)
	c, _ = sharedDecode(raw)
	a := &sharedTestAuthority{binding: c["binding"].(map[string]any), active: true}
	browser.g.Shared = &SharedEntry{Authority: a, Store: &sharedTestStore{used: map[string]bool{}, cookies: map[string]SharedBrowserBinding{}}, TicketKeys: map[string]ed25519.PublicKey{"test": pub}, IdentityKey: priv, IdentityKid: "test"}
	token := sharedTestSign(c, priv, false)
	resp, err := browser.client.Post(browser.root+instanceMount(sharedTestID)+"_auriga/placement-ticket", "application/json", strings.NewReader(`{"ticket":"`+token+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	readIPResponse(t, resp, 302, "")
	r, _ := http.NewRequest("GET", browser.root+instanceMount(sharedTestID)+"api?x=1", nil)
	r.Header.Add("Auriga-Identity", "forged")
	r.Header.Add("Auriga-Identity", "forged2")
	resp, err = browser.client.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	readIPResponse(t, resp, 204, "")
	got := <-seen
	if got["canonicalPath"] != "/api" || !sharedEqual(got["binding"], c["binding"]) {
		t.Fatal("wrong forwarded identity")
	}
	a.mu.Lock()
	a.binding["epoch"] = 2
	a.mu.Unlock()
	resp = browser.get(t, instanceMount(sharedTestID)+"api")
	readIPResponse(t, resp, 403, "shared.binding_mismatch")
	// A cookie scoped to instance A cannot authorize instance B.
	resp = browser.get(t, instanceMount(pathInstanceB)+"api")
	readIPResponse(t, resp, 403, "")
}
