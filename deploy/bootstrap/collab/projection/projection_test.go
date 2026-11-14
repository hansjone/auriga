package projection

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	api "github.com/hansjone/auriga/infra/gen/msgnetapi"
)

func fixture(t *testing.T, name string, out any) {
	t.Helper()
	b, e := os.ReadFile(filepath.Join("../../../..", "contracts/network/fixtures", name))
	if e != nil {
		t.Fatal(e)
	}
	if e = json.Unmarshal(b, out); e != nil {
		t.Fatal(e)
	}
}
func verdict(e error) string {
	if e == nil {
		return "accept"
	}
	var f Fault
	if errors.As(e, &f) {
		return string(f)
	}
	return e.Error()
}
func TestLocalVectors(t *testing.T) {
	var cases []struct {
		ID, Expected string
		Input        struct {
			Request                   api.LocalProjectionRequest
			Manifest                  api.LocalProjectionManifest
			Files                     map[string]string
			Published                 api.LocalProjectionSelector
			Previous                  *api.LocalProjectionSelector
			Persisted, Loaded, Failed bool
		}
	}
	fixture(t, "msgnet-local-projection-vectors.json", &cases)
	for _, v := range cases {
		t.Run(v.ID, func(t *testing.T) {
			files := map[string][]byte{}
			for n, b := range v.Input.Files {
				files[n] = []byte(b)
			}
			e := CheckFiles(v.Input.Request.Selector, v.Input.Manifest, files, v.Input.Published, v.Input.Previous)
			if e == nil && (!v.Input.Persisted || (v.Input.Request.Operation == "confirm" && !v.Input.Loaded) || v.Input.Failed) {
				e = Fault("projection_pending")
			}
			if got := verdict(e); got != v.Expected {
				t.Fatalf("got %s want %s", got, v.Expected)
			}
		})
	}
}
func TestConsumerVectors(t *testing.T) {
	var vectors struct {
		Cases []struct {
			ID, Expected string
			Input        json.RawMessage
		}
	}
	fixture(t, "auto-renewal-v2-vectors.json", &vectors)
	for _, v := range vectors.Cases {
		t.Run(v.ID, func(t *testing.T) {
			var i struct {
				Root              string `json:"root_spki"`
				Tenant            string `json:"tenant_id"`
				Realm, Now        string
				Latest            api.SignedRootControl  `json:"latest_control"`
				SeenControl       string                 `json:"seen_control_revision"`
				SeenGeneration    string                 `json:"seen_generation"`
				SeenVersion       string                 `json:"seen_version"`
				SeenDigest        string                 `json:"seen_digest"`
				PreviousDigest    string                 `json:"previous_digest"`
				First             []api.FirstPublication `json:"first_publication_ledger"`
				Publication       api.Publication
				Transport         *api.SignedTransportTrustBundle
				SeenBundle        string                          `json:"seen_bundle_version"`
				PreviousPhase     string                          `json:"previous_transport_phase"`
				PreviousTransport *api.SignedTransportTrustBundle `json:"previous_transport"`
				Readers           bool                            `json:"readers_persisted"`
				Handshakes        bool                            `json:"handshakes_verified"`
			}
			if e := json.Unmarshal(v.Input, &i); e != nil {
				t.Fatal(e)
			}
			f := Facts{RootSPKI: i.Root, Tenant: i.Tenant, Realm: i.Realm, Now: stamp(i.Now), Latest: i.Latest, SeenControl: i.SeenControl, SeenGeneration: i.SeenGeneration, SeenVersion: i.SeenVersion, SeenDigest: i.SeenDigest, PreviousDigest: i.PreviousDigest, First: i.First}
			e := VerifyPublication(i.Publication, f)
			if e == nil && i.Transport != nil {
				e = VerifyTransport(*i.Transport, i.Publication, TransportFacts{ControlRevision: i.Latest.Payload.ControlRevision, SeenVersion: i.SeenBundle, PreviousPhase: i.PreviousPhase, Previous: i.PreviousTransport, ReadersPersisted: i.Readers, HandshakesVerified: i.Handshakes})
			}
			if got := verdict(e); got != v.Expected {
				t.Fatalf("got %s want %s", got, v.Expected)
			}
		})
	}
}
func TestRejectAmbiguousJSON(t *testing.T) {
	for _, raw := range []string{`{"a":1,"a":2}`, `{} {}`, `{"a":0.1}`, `{"a":9007199254740992}`, `{"a":"\ud800"}`, `{"a":"\udc00"}`} {
		if _, e := Canonical([]byte(raw)); e == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
	if _, e := Canonical([]byte{0xff}); e == nil {
		t.Fatal("invalid UTF-8 accepted")
	}
}
func TestResponseExactSelector(t *testing.T) {
	var cases []struct {
		Input struct{ Request api.LocalProjectionRequest }
	}
	fixture(t, "msgnet-local-projection-vectors.json", &cases)
	s := cases[0].Input.Request.Selector
	for _, phase := range []string{"loaded", "handshake_verified", "failed"} {
		code := "none"
		if phase == "failed" {
			code = "projection_pending"
		}
		got, p, c, e := ResponseFields(response(s, phase, code))
		if e != nil || !Equal(got, s) || p != phase || c != code {
			t.Fatalf("response %s: %v", phase, e)
		}
	}
}
