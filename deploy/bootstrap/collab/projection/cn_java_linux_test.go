package projection

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	api "github.com/hansjone/auriga/infra/gen/msgnetapi"
)

type cnTransport struct {
	inner    http.RoundTripper
	mutation string
	statuses map[string][]int
}

func (r *cnTransport) RoundTrip(q *http.Request) (*http.Response, error) {
	res, err := r.inner.RoundTrip(q)
	if err != nil {
		return nil, err
	}
	r.statuses[q.URL.Path] = append(r.statuses[q.URL.Path], res.StatusCode)
	if r.mutation != "" && q.URL.Path == "/api/collab/v2/network/transport/expected-selectors" && res.StatusCode == http.StatusOK {
		raw, err := io.ReadAll(res.Body)
		_ = res.Body.Close()
		if err != nil {
			return nil, err
		}
		var value map[string]any
		if err = json.Unmarshal(raw, &value); err != nil {
			return nil, err
		}
		if r.mutation == "source" {
			value["control_digest"] = Digest([]byte("different-source"))
		}
		if r.mutation == "expired" {
			value["issued_at"] = stamp(value["issued_at"].(string)).Add(-61 * time.Second).Format(time.RFC3339)
		}
		raw, err = json.Marshal(value)
		if err != nil {
			return nil, err
		}
		res.Body = io.NopCloser(bytes.NewReader(raw))
		res.ContentLength = int64(len(raw))
	}
	return res, nil
}

// Opt-in integration uses the fixed Java artifact and C's real PG/MVC/security
// implementation. The normal test suite does not start an external server.
func TestCNJavaManagement(t *testing.T) {
	run := os.Getenv("AURIGA_MSGNET_CN_FIXTURE")
	if run == "" {
		t.Skip("requires integration/run-cn.ps1 and the verified C artifact")
	}
	var meta struct{ Origin, Tenant, Now, Root string }
	raw, err := os.ReadFile(filepath.Join(run, "fixture.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(raw, &meta); err != nil {
		t.Fatal(err)
	}
	origin, err := url.Parse(meta.Origin)
	if err != nil || origin.Scheme != "https" || origin.Hostname() != "127.0.0.1" {
		t.Fatal("local HTTPS fixture required")
	}
	schemas, err := LoadSchemas("../../../../contracts/network/auto-renewal-v2.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	client, err := NewTransportHTTP(meta.Origin, filepath.Join(run, "ca.pem"), filepath.Join(run, "client.pem"), filepath.Join(run, "client.key"), filepath.Join(run, "service.jwt"))
	if err != nil {
		t.Fatal(err)
	}
	trace := &cnTransport{inner: client.projector.ReceiptClient.Transport, statuses: map[string][]int{}}
	client.projector.ReceiptClient.Transport = trace
	t.Cleanup(func() {
		body, marshalErr := json.MarshalIndent(trace.statuses, "", "  ")
		if marshalErr != nil {
			t.Error(marshalErr)
			return
		}
		if writeErr := os.WriteFile(filepath.Join(run, "status-"+os.Getenv("AURIGA_MSGNET_CN_MODE")+".json"), body, 0600); writeErr != nil {
			t.Error(writeErr)
		}
	})
	ctx := context.Background()
	get := func(path, name string, out any) {
		t.Helper()
		data, getErr := client.Request(ctx, "GET", "/api/collab/v2/network/"+path, nil)
		if getErr != nil {
			t.Fatal(getErr)
		}
		if getErr = schemas.Decode(name, data, out); getErr != nil {
			t.Fatal(getErr)
		}
	}
	var publication api.Publication
	var bundle api.SignedTransportTrustBundle
	get("bootstrap/keys", "Publication", &publication)
	get("bootstrap/transport", "SignedTransportTrustBundle", &bundle)
	now := stamp(meta.Now)
	if err = VerifyPublication(publication, Facts{RootSPKI: meta.Root, Tenant: meta.Tenant, Realm: "1", Now: now, Latest: publication.Control}); err != nil {
		t.Fatal("independent C root chain", err)
	}
	source := &TransportSource{Schemas: schemas, Root: meta.Root, Tenant: meta.Tenant, Realm: "1", Target: "target", Request: client.Request, ExpectedSelectors: client.ExpectedSelectors, Now: func() time.Time { return now }}
	if err = VerifyTransport(bundle, publication, TransportFacts{ControlRevision: publication.Control.Payload.ControlRevision, ReadersPersisted: true, HandshakesVerified: true}); err != nil {
		t.Fatal(err)
	}
	challenge := make([]byte, 32)
	if _, err = rand.Read(challenge); err != nil {
		t.Fatal(err)
	}
	digest, _ := ObjectDigest(bundle)
	controlDigest, _ := ObjectDigest(publication.Control)
	query := api.ExpectedSelectorsQuery{TenantId: meta.Tenant, Realm: "1", BundleVersion: bundle.Payload.BundleVersion, BundleDigest: digest, ControlRevision: publication.Control.Payload.ControlRevision, ControlDigest: controlDigest, Challenge: base64.RawURLEncoding.EncodeToString(challenge)}
	ledgerRaw, err := client.ExpectedSelectors(ctx, query)
	if err != nil {
		t.Fatal("actual C ledger", err)
	}
	var ledger api.ExpectedSelectors
	if err = schemas.Decode("ExpectedSelectors", ledgerRaw.body, &ledger); err != nil {
		t.Fatal(err)
	}
	if err = VerifyExpectedSelectors(ledger, query, bundle, publication.Control, SelectorLedgerFacts{Tenant: meta.Tenant, Realm: "1", Now: now, Authenticated: ledgerRaw.authenticated}); err != nil {
		t.Fatal(err)
	}
	if os.Getenv("AURIGA_MSGNET_CN_MODE") == "pending" {
		if value, exists := ledger.ExpectedSelectors["synthetic-projector"]; !exists || value != nil {
			t.Fatal("C must distinguish explicit unassigned null")
		}
		confirmation := api.TransportConfirmationQuery{TenantId: meta.Tenant, Realm: "1", BundleVersion: bundle.Payload.BundleVersion, BundleDigest: digest, Challenge: query.Challenge}
		if _, err = client.Request(ctx, "POST", "/api/collab/v2/network/transport/confirmation", confirmation); err != Fault("projection_pending") {
			t.Fatal("202 did not block", err)
		}
		codes := trace.statuses["/api/collab/v2/network/transport/confirmation"]
		if len(codes) != 1 || codes[0] != http.StatusAccepted {
			t.Fatal("C did not return actual 202", codes)
		}
		for _, tokenFile := range []string{"unprivileged.jwt", ""} {
			bad, makeErr := NewTransportHTTP(meta.Origin, filepath.Join(run, "ca.pem"), filepath.Join(run, "client.pem"), filepath.Join(run, "client.key"), filepath.Join(run, tokenFile))
			if makeErr != nil {
				t.Fatal(makeErr)
			}
			if tokenFile == "" {
				bad.projector.ServiceTokenFile = ""
			}
			badTrace := &cnTransport{inner: bad.projector.ReceiptClient.Transport, statuses: map[string][]int{}}
			bad.projector.ReceiptClient.Transport = badTrace
			if _, makeErr = bad.Request(ctx, "POST", "/api/collab/v2/network/transport/expected-selectors", query); makeErr == nil {
				t.Fatal("unauthorized C ledger accepted")
			}
			status := badTrace.statuses["/api/collab/v2/network/transport/expected-selectors"]
			want := http.StatusForbidden
			if tokenFile == "" {
				want = http.StatusUnauthorized
			}
			if len(status) != 1 || status[0] != want {
				t.Fatal("authorization status", status, "want", want)
			}
			t.Logf("actual C unauthorized response %d", status[0])
		}
		t.Log("actual C null ledger 200 and pending confirmation 202 verified")
		return
	}
	var first api.SignedTransportTrustBundle
	raw, err = os.ReadFile(filepath.Join(run, "first-bundle.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err = schemas.Decode("SignedTransportTrustBundle", raw, &first); err != nil {
		t.Fatal(err)
	}
	if bundle.Payload.Phase != "new_leaves" {
		t.Fatal("expected real C new_leaves", bundle.Payload.Phase)
	}
	if err = source.Verify(ctx, publication, bundle, publication.Control, &first, now); err != nil {
		t.Fatal("actual C complete gate", err)
	}
	for _, mutation := range []string{"source", "expired"} {
		trace.mutation = mutation
		if err = source.Verify(ctx, publication, bundle, publication.Control, &first, now); err != Fault("projection_pending") {
			t.Fatal("C response fault injection", mutation, err)
		}
		t.Log("rejected actual C response after fault injection", mutation)
	}
	trace.mutation = ""
	unprivileged, err := NewTransportHTTP(meta.Origin, filepath.Join(run, "ca.pem"), filepath.Join(run, "client.pem"), filepath.Join(run, "client.key"), filepath.Join(run, "unprivileged.jwt"))
	if err != nil {
		t.Fatal(err)
	}
	unprivileged.projector.ReceiptClient.Transport = &cnTransport{inner: unprivileged.projector.ReceiptClient.Transport, statuses: trace.statuses}
	source.ExpectedSelectors = unprivileged.ExpectedSelectors
	if err = source.Verify(ctx, publication, bundle, publication.Control, &first, now); err != Fault("projection_pending") {
		t.Fatal("replaced ledger authentication advanced gate", err)
	}
	t.Log("rejected replacement ledger client after actual C authorization failure")
	source.ExpectedSelectors = client.ExpectedSelectors
	if err = source.Verify(ctx, publication, bundle, publication.Control, &first, now); err != nil {
		t.Fatal("recover after rejected map", err)
	}
	t.Log("actual C 200 ledger and signed confirmation advanced N new_leaves gate")
}
