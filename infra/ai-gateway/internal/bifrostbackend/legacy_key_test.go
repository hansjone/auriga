package bifrostbackend

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/hansjone/auriga/infra/ai-gateway/internal/testpg"
	api "github.com/hansjone/auriga/infra/gen/aiapi"
)

func TestLegacyKeyBindingPreservesCallerCredentialAndRevocation(t *testing.T) {
	db := testpg.Open(t)
	master := make([]byte, 32)
	if _, err := rand.Read(master); err != nil {
		t.Fatal(err)
	}
	password, memberPassword := rand.Text()+rand.Text(), rand.Text()+rand.Text()
	b, err := New(db, hex.EncodeToString(master), password, memberPassword, true)
	if err != nil {
		t.Fatal(err)
	}
	b.tenant, _ = newID()
	key, err := b.SaveKey(t.Context(), b.tenant, "", api.RelayKeyInput{Name: "imported", Active: true, Models: []string{"*"}})
	if err != nil {
		t.Fatal(err)
	}
	old := "sk-" + rand.Text()
	if _, _, err := b.parseToken(old); err == nil {
		t.Fatal("unbound legacy key accepted")
	}
	hash := sha256.Sum256([]byte(old))
	body := fmt.Sprintf(`{"keyId":%q,"sha256":%q}`, key.Key.Id, hex.EncodeToString(hash[:]))
	call := func(tenant, auth, value string) int {
		r := httptest.NewRequest("PUT", "/api/legacy-key-bindings", strings.NewReader(value))
		r.SetBasicAuth("auriga", auth)
		r.Header.Set("X-Auriga-Tenant", tenant)
		w := httptest.NewRecorder()
		b.ServeHTTP(w, r)
		return w.Code
	}
	if code := call(b.tenant, "wrong", body); code != 401 {
		t.Fatal(code)
	}
	other, _ := newID()
	if code := call(other, password, body); code != 403 {
		t.Fatal(code)
	}
	for range 2 {
		if code := call(b.tenant, password, body); code != 200 {
			t.Fatal(code)
		}
	}
	tenant, id, err := b.parseToken(old)
	if err != nil || tenant != b.tenant || id != key.Key.Id {
		t.Fatal("legacy credential not resolved", err)
	}
	if _, _, err = b.parseToken(old + "x"); err == nil {
		t.Fatal("forged alias accepted")
	}
	second, err := b.SaveKey(t.Context(), b.tenant, "", api.RelayKeyInput{Name: "another", Active: true, Models: []string{"*"}})
	if err != nil {
		t.Fatal(err)
	}
	changed := strings.ReplaceAll(body, key.Key.Id, second.Key.Id)
	if code := call(b.tenant, password, changed); code != 409 {
		t.Fatal(code)
	}
	expired := "sk-" + rand.Text()
	expiredHash := sha256.Sum256([]byte(expired))
	expiredBody := fmt.Sprintf(`{"keyId":%q,"sha256":%q,"expiresAt":%q}`, key.Key.Id, hex.EncodeToString(expiredHash[:]), time.Now().Add(-time.Hour).UTC().Format(time.RFC3339))
	if code := call(b.tenant, password, expiredBody); code != 200 {
		t.Fatal(code)
	}
	if _, _, err := b.parseToken(expired); err == nil {
		t.Fatal("expired legacy key accepted")
	}
	account, _ := newID()
	member, err := b.EnsureMember(t.Context(), api.RelayMemberEnsure{TenantId: b.tenant, AccountId: account})
	if err != nil {
		t.Fatal(err)
	}
	memberBody := strings.ReplaceAll(body, key.Key.Id, member.Key.Id)
	if code := call(b.tenant, password, memberBody); code != 403 {
		t.Fatal("member identity bridge bypass", code)
	}
	if _, err = b.SaveKey(t.Context(), b.tenant, key.Key.Id, api.RelayKeyInput{Name: "imported", Active: false, Models: []string{"*"}}); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("GET", "/v1/models", nil)
	r.Header.Set("Authorization", "Bearer "+old)
	w := httptest.NewRecorder()
	b.ServeHTTP(w, r)
	if w.Code == 200 {
		t.Fatal("revoked key still usable")
	}
}

func TestLegacyMissCacheBoundedExpiresAndBindingWins(t *testing.T) {
	var cache legacyMissCache
	now := time.Now()
	digest := sha256.Sum256([]byte(rand.Text()))
	_, generation := cache.lookup(digest, now)
	cache.remember(digest, generation, now)
	if hit, _ := cache.lookup(digest, now.Add(time.Second)); !hit {
		t.Fatal("miss was not cached")
	}
	if hit, _ := cache.lookup(digest, now.Add(3*time.Second)); hit {
		t.Fatal("negative cache did not expire")
	}
	cache.invalidate(digest)
	cache.remember(digest, generation, now)
	if hit, _ := cache.lookup(digest, now); hit {
		t.Fatal("in-flight lookup hid a new binding")
	}
	for i := range 1100 {
		digest := sha256.Sum256(fmt.Appendf(nil, "unknown-%d", i))
		_, generation := cache.lookup(digest, now)
		cache.remember(digest, generation, now)
	}
	if len(cache.entries) > 1024 {
		t.Fatal("negative cache grew without a bound")
	}
}
