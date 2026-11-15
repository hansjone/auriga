package ingress

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"

	"testing"
	"time"
)

const sharedTestID = "019a0000-0000-7000-8000-000000000001"

func sharedTestClaims(identity bool, now int64) map[string]any {
	b := map[string]any{"tenantId": sharedTestID, "memberId": sharedTestID, "placementId": sharedTestID, "physicalInstanceId": sharedTestID, "mode": "shared", "epoch": 1, "revision": 1}
	m := map[string]any{"schemaVersion": 1, "binding": b, "platformSessionId": sharedTestID, "audience": "auriga.placement.exchange", "issuedAt": now, "expiresAt": now + 60, "jti": sharedTestID}
	if identity {
		m["schemaVersion"] = 2
		m["issuer"] = "auriga.edge"
		m["audience"] = "auriga.shared.ingress"
		m["method"] = "GET"
		m["canonicalPath"] = "/api"
	}
	return m
}
func sharedTestSign(m map[string]any, key ed25519.PrivateKey, identity bool) string {
	typ := "auriga-shared-ticket+jwt"
	if identity {
		typ = "auriga-shared-identity+jwt"
	}
	h, _ := json.Marshal(map[string]string{"alg": "EdDSA", "kid": "test", "typ": typ})
	p, _ := json.Marshal(m)
	s := base64.RawURLEncoding.EncodeToString(h) + "." + base64.RawURLEncoding.EncodeToString(p)
	return s + "." + base64.RawURLEncoding.EncodeToString(ed25519.Sign(key, []byte(s)))
}
func TestSharedWireNegative(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	now := time.Now().Unix()
	keys := map[string]ed25519.PublicKey{"test": pub}
	for _, identity := range []bool{false, true} {
		t.Run(map[bool]string{true: "identity", false: "ticket"}[identity], func(t *testing.T) {
			c := sharedTestClaims(identity, now)
			tok := sharedTestSign(c, priv, identity)
			if _, err := sharedVerify(tok, keys, identity, now); err != nil {
				t.Fatal(err)
			}
			for _, mutation := range []func(map[string]any){func(c map[string]any) { c["audience"] = "other" }, func(c map[string]any) { c["expiresAt"] = now }, func(c map[string]any) { c["issuedAt"] = now + 1 }, func(c map[string]any) { c["expiresAt"] = now + 61 }, func(c map[string]any) { c["extra"] = true }, func(c map[string]any) { c["binding"].(map[string]any)["epoch"] = 0 }, func(c map[string]any) { c["binding"].(map[string]any)["revision"] = 1.5 }} {
				c = sharedTestClaims(identity, now)
				mutation(c)
				if _, err := sharedVerify(sharedTestSign(c, priv, identity), keys, identity, now); err == nil {
					t.Fatal("accepted mutation")
				}
			}
			if _, err := sharedVerify(tok, keys, !identity, now); err == nil {
				t.Fatal("cross-purpose")
			}
		})
	}
	for _, raw := range []string{`{"a":1,"a":1}`, `{ "a":1}`, `{"b":1,"a":1}`, `{"a":1} {}`} {
		if _, err := sharedDecode([]byte(raw)); err == nil {
			t.Fatal("accepted noncanonical", raw)
		}
	}
	for _, p := range []string{"//api", "/a/../b", "/%2e%2e", "/%2f", "/%5c", "/%zz", "/a\\b", "/a?b", "/%252e"} {
		if sharedPath(p) {
			t.Fatal("accepted path", p)
		}
	}
}
