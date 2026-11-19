package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func runtimeProfileBody(mode, pool string) map[string]any {
	capabilities := map[string]any{"pluginInstall": true, "privateSkills": true, "includeDefaultRoots": true, "toolPresentation": "native"}
	if mode == "shared" {
		capabilities = map[string]any{"shell": false, "terminal": false, "jobs": false, "subagent": false, "stdioMcp": false, "pluginInstall": false, "privateSkills": false, "personalKeys": false, "includeDefaultRoots": false, "toolPresentation": "native"}
	}
	return map[string]any{"schemaVersion": 1, "physicalInstanceId": pool, "profile": "auriga." + mode + ".v1", "capabilities": capabilities}
}

func TestDedicatedProfileRequiresModePathAndExactCapabilitiesOnTheNormalEntry(t *testing.T) {
	public, key, _ := ed25519.GenerateKey(rand.Reader)
	var requests atomic.Int32
	var failure atomic.Value
	failure.Store("")
	browser := newIPBrowser(t, false, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Method != http.MethodGet || r.URL.Path != "/i/"+sharedTestID+dedicatedProfilePath || r.TLS == nil || len(r.TLS.VerifiedChains) == 0 || r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" || r.Header.Get("Auriga-Identity") != "" || r.Header.Get(sharedProfileHeader) != "" {
			t.Error("dedicated profile escaped its fixed authenticated Node hop")
		}
		body := runtimeProfileBody("dedicated", sharedTestID)
		switch failure.Load().(string) {
		case "shared":
			body = runtimeProfileBody("shared", sharedTestID)
		case "extra":
			body["capabilities"].(map[string]any)["shell"] = true
		case "disabled":
			body["capabilities"].(map[string]any)["pluginInstall"] = false
		case "cookie":
			w.Header().Set("Set-Cookie", "unexpected=1")
		case "redirect":
			w.Header().Set("Location", "/owner")
		case "oversize":
			body["padding"] = strings.Repeat("x", 16385)
		}
		w.Header().Set("Content-Type", "application/json")
		data, _ := sharedMarshal(body)
		_, _ = w.Write(data)
	}))
	browser.g.Shared = &SharedEntry{Authority: &sharedTestAuthority{active: false}, Store: &sharedTestStore{used: map[string]bool{}, cookies: map[string]SharedBrowserBinding{}}, TicketKeys: map[string]ed25519.PublicKey{"test": public}, IdentityKey: key, IdentityKid: "test"}
	client := *browser.client
	client.Jar = nil
	claims := func(mode string) map[string]any {
		now := time.Now().Unix()
		binding := sharedTestClaims(false, now)["binding"].(map[string]any)
		binding["tenantId"], binding["mode"] = browser.g.Cfg.Access.TenantID(), mode
		return map[string]any{"schemaVersion": 1, "binding": binding, "audience": "auriga.shared.profile-probe", "issuedAt": now, "expiresAt": now + 10, "jti": sharedJTI()}
	}
	request := func(method, path, mode string, extra http.Header) *http.Response {
		r, err := http.NewRequest(method, browser.root+instanceMount(sharedTestID)+strings.TrimPrefix(path, "/"), nil)
		if err != nil {
			t.Fatal(err)
		}
		r.Header.Set(sharedProfileHeader, sharedProfileProof(t, claims(mode), key, "auriga-shared-profile-probe+jwt"))
		for name, values := range extra {
			r.Header[name] = values
		}
		response, err := client.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		return response
	}
	readIPResponse(t, request("GET", dedicatedProfilePath, "dedicated", nil), 200, "")
	for _, test := range []struct {
		method, path, mode string
		headers            http.Header
	}{
		{"GET", dedicatedProfilePath, "shared", nil}, {"GET", sharedProfilePath, "dedicated", nil},
		{"POST", dedicatedProfilePath, "dedicated", nil}, {"GET", dedicatedProfilePath + "?x=1", "dedicated", nil},
		{"GET", dedicatedProfilePath, "dedicated", http.Header{"Cookie": {"owner=1"}}},
		{"GET", dedicatedProfilePath, "dedicated", http.Header{"Authorization": {"Bearer owner"}}},
	} {
		readIPResponse(t, request(test.method, test.path, test.mode, test.headers), 403, "shared.binding_mismatch")
	}
	if requests.Load() != 1 {
		t.Fatal("mismatched proof/method/path reached the Host")
	}
	for _, invalid := range []string{"shared", "extra", "disabled", "cookie", "redirect", "oversize"} {
		failure.Store(invalid)
		readIPResponse(t, request("GET", dedicatedProfilePath, "dedicated", nil), 503, "common.unavailable")
	}
}

func TestRuntimeProfilesCannotBeInterchangedOrExtended(t *testing.T) {
	for _, mode := range []string{"shared", "dedicated"} {
		data, err := sharedMarshal(runtimeProfileBody(mode, sharedTestID))
		if err != nil {
			t.Fatal(err)
		}
		profile, err := sharedDecode(data)
		if err != nil {
			t.Fatal(err)
		}
		if !validRuntimeProfile(profile, sharedTestID, mode) {
			t.Fatal("exact profile rejected", mode)
		}
		other := "shared"
		if mode == other {
			other = "dedicated"
		}
		if validRuntimeProfile(profile, sharedTestID, other) {
			t.Fatal("mode profile confusion")
		}
		profile["capabilities"].(map[string]any)["unexpected"] = false
		if validRuntimeProfile(profile, sharedTestID, mode) {
			t.Fatal("extra capability accepted")
		}
	}
}
