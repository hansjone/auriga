package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func sharedProfileProof(t *testing.T, claims map[string]any, key ed25519.PrivateKey, typ string) string {
	t.Helper()
	header, err := sharedMarshal(map[string]string{"alg": "EdDSA", "kid": "test", "typ": typ})
	if err != nil {
		t.Fatal(err)
	}
	body, err := sharedMarshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	input := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(body)
	return input + "." + base64.RawURLEncoding.EncodeToString(ed25519.Sign(key, []byte(input)))
}

func TestSharedDedicatedExchangeRequiresCurrentPlacementAuthority(t *testing.T) {
	public, key, _ := ed25519.GenerateKey(rand.Reader)
	now := time.Now()
	claims := sharedTestClaims(false, now.Unix())
	claims["binding"].(map[string]any)["mode"] = "dedicated"
	raw, _ := json.Marshal(claims)
	claims, _ = sharedDecode(raw)
	authority := &sharedTestAuthority{binding: claims["binding"].(map[string]any), active: true}
	entry := &SharedEntry{Authority: authority, Store: &sharedTestStore{used: map[string]bool{}, cookies: map[string]SharedBrowserBinding{}}, TicketKeys: map[string]ed25519.PublicKey{"test": public}}
	target := InstanceTarget{"https://portal.example.test", "/i/" + sharedTestID + "/"}
	exchange := func() *httptest.ResponseRecorder {
		token := sharedTestSign(claims, key, false)
		request := httptest.NewRequest(http.MethodPost, target.Origin+target.Mount+"_auriga/placement-ticket", strings.NewReader(`{"ticket":"`+token+`"}`))
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		entry.exchange(response, request, sharedTestID, target, true, now)
		return response
	}
	if response := exchange(); response.Code != http.StatusFound || response.Header().Get("Location") != target.Mount {
		t.Fatal("approved dedicated placement did not enter its own pool")
	}
	authority.active = false
	claims["jti"] = sharedJTI()
	if response := exchange(); response.Code != http.StatusForbidden || len(response.Result().Cookies()) != 0 {
		t.Fatal("revoked dedicated placement acquired a browser binding")
	}
}

func TestSharedInitialProfileUsesFixedMTLSReadBeforePlacementReady(t *testing.T) {
	public, key, _ := ed25519.GenerateKey(rand.Reader)
	var requests atomic.Int32
	var unsafeProfile atomic.Bool
	browser := newIPBrowser(t, false, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Method != http.MethodGet || r.URL.Path != "/i/"+sharedTestID+sharedProfilePath || r.URL.RawQuery != "" ||
			r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" || r.Header.Get("Auriga-Identity") != "" || r.Header.Get(sharedProfileHeader) != "" ||
			r.TLS == nil || len(r.TLS.VerifiedChains) == 0 {
			t.Error("initial profile read escaped the fixed authenticated node hop")
		}
		capabilities := map[string]any{
			"shell": false, "terminal": false, "jobs": false, "subagent": false,
			"stdioMcp": false, "pluginInstall": false, "privateSkills": false,
			"personalKeys": false, "includeDefaultRoots": false, "toolPresentation": "native",
		}
		capabilities["shell"] = unsafeProfile.Load()
		body, _ := sharedMarshal(map[string]any{"schemaVersion": 1, "physicalInstanceId": sharedTestID, "profile": "auriga.shared.v1", "capabilities": capabilities})
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	// No member entry is ready yet. The profile proof can only read static
	// installation facts, so it never invokes a ready-only member Resolve.
	browser.g.Shared = &SharedEntry{
		Authority:  &sharedTestAuthority{active: false},
		Store:      &sharedTestStore{used: map[string]bool{}, cookies: map[string]SharedBrowserBinding{}},
		TicketKeys: map[string]ed25519.PublicKey{"test": public}, IdentityKey: key, IdentityKid: "test",
	}
	client := *browser.client
	client.Jar = nil
	claims := func() map[string]any {
		now := time.Now().Unix()
		binding := sharedTestClaims(false, now)["binding"].(map[string]any)
		binding["tenantId"] = browser.g.Cfg.Access.TenantID()
		return map[string]any{"schemaVersion": 1, "binding": binding, "audience": "auriga.shared.profile-probe", "issuedAt": now, "expiresAt": now + 10, "jti": sharedJTI()}
	}
	request := func(proof, suffix string) *http.Response {
		r, err := http.NewRequest(http.MethodGet, browser.root+instanceMount(sharedTestID)+"_auriga/shared-profile"+suffix, nil)
		if err != nil {
			t.Fatal(err)
		}
		r.Header.Set(sharedProfileHeader, proof)
		response, err := client.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		return response
	}
	valid := sharedProfileProof(t, claims(), key, "auriga-shared-profile-probe+jwt")
	readIPResponse(t, request(valid, ""), 200, "")
	if requests.Load() != 1 {
		t.Fatal("profile did not reach its actual trusted runtime")
	}
	readIPResponse(t, request(sharedProfileProof(t, claims(), key, "auriga-shared-ticket+jwt"), ""), 401, "shared.signature_invalid")
	readIPResponse(t, request(valid, "?other=path"), 403, "shared.binding_mismatch")
	for _, mutate := range []func(map[string]any){
		func(c map[string]any) { c["expiresAt"] = time.Now().Unix() + 11 },
		func(c map[string]any) { c["expiresAt"] = time.Now().Unix() - 1 },
		func(c map[string]any) { c["binding"].(map[string]any)["physicalInstanceId"] = pathInstanceB },
		func(c map[string]any) { c["binding"].(map[string]any)["tenantId"] = pathInstanceB },
	} {
		changed := claims()
		mutate(changed)
		response := request(sharedProfileProof(t, changed, key, "auriga-shared-profile-probe+jwt"), "")
		if response.StatusCode == http.StatusOK {
			t.Fatal("invalid profile scope or lifetime was accepted")
		}
		_ = response.Body.Close()
	}
	if requests.Load() != 1 {
		t.Fatal("rejected proof reached the runtime")
	}
	unsafeProfile.Store(true)
	body := readIPResponse(t, request(valid, ""), 503, "common.unavailable")
	if strings.Contains(body, valid) {
		t.Fatal("probe proof appeared in an error response")
	}
}
