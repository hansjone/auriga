package network

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	collab "github.com/hansjone/auriga/infra/gen/auriga/collab/v1"
	iam "github.com/hansjone/auriga/infra/gen/auriga/iam/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const submitTestJSON = `{"kind":"message","to":{"realm":"2","node":"65","local":"1"},"intent":"information","durability":"durable","future_optional":{"keep":true},"body":{"subtype":"text","text":"hello","optional":{"keep":true}},"source":{"source_binding":"source-binding-01"},"idempotency":{"producer_epoch":"1","key":"send-key-0000001"}}`

func (f *fakeMailboxRPC) Submit(ctx context.Context, q *collab.NetworkMailboxServiceSubmitRequest) (*collab.NetworkMailboxServiceSubmitResponse, error) {
	b, e := f.call(ctx, "submit", q.Connection, q.SubmitJson)
	return &collab.NetworkMailboxServiceSubmitResponse{ResultJson: b}, e
}

func TestSubmitHTTPSIdentityBoundary(t *testing.T) {
	f := &fakeMailboxRPC{response: []byte(`{ "status":"platform_stored", "from":{"node":"64"}, "opaque":"original" }`)}
	conn, err := dialMailboxRPC(startMailboxRPC(t, f))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	client, err := NewMailboxClient(conn, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	b := liveBinding{Binding: Binding{Ingress: ingressForTicket("submit"), ServerID: "server", ClientID: 7, Account: clientAccount}, Tenant: testTenant, Realm: "2", Node: "64", Device: testDevice, Epoch: 9, NotAfter: time.Now().Add(time.Minute)}
	r := &IAMRedeemer{bindings: map[string]liveBinding{b.Ingress: b}}
	auth := func(_ context.Context, token string) (*iam.VerifyDeviceAccessResponse, error) {
		return &iam.VerifyDeviceAccessResponse{Active: token == "valid", TenantId: testTenant, DeviceId: testDevice, AccountId: testTenant, ExpiresAt: timestamppb.New(time.Now().Add(time.Minute))}, nil
	}
	h := newSubmitHandler(r, client, auth)
	srv := httptest.NewTLSServer(h)
	defer srv.Close()
	post := func(token, body string, headers map[string]string) (int, string) {
		t.Helper()
		q, e := http.NewRequest(http.MethodPost, srv.URL+SubmitPath, strings.NewReader(body))
		if e != nil {
			t.Fatal(e)
		}
		q.Header.Set("Authorization", "Bearer "+token)
		q.Header.Set("Content-Type", "application/json")
		for k, v := range headers {
			q.Header.Set(k, v)
		}
		p, e := srv.Client().Do(q)
		if e != nil {
			t.Fatal(e)
		}
		defer func() { _ = p.Body.Close() }()
		var out strings.Builder
		_, e = io.Copy(&out, p.Body)
		if e != nil {
			t.Fatal(e)
		}
		return p.StatusCode, out.String()
	}
	code, body := post("valid", submitTestJSON, nil)
	if code != 200 || body != string(f.response) {
		t.Fatalf("%d %s", code, body)
	}
	if len(f.identities) != 1 || f.identities[0].TenantId != testTenant || f.identities[0].Node != "64" || f.identities[0].ClientId != "7" || string(f.frames[0]) != submitTestJSON {
		t.Fatal("trusted identity or original request lost")
	}
	for _, bad := range []string{
		strings.Replace(submitTestJSON, `"kind":`, `"tenant":"other","kind":`, 1),
		strings.Replace(submitTestJSON, `"source_binding":`, `"node":"65","source_binding":`, 1),
		strings.Replace(submitTestJSON, `"kind":`, `"node":"65","kind":`, 1),
		strings.Replace(submitTestJSON, `"kind":`, `"from":{},"kind":`, 1),
		strings.Replace(submitTestJSON, `"kind":`, `"kind":"message","kind":`, 1),
	} {
		if c, _ := post("valid", bad, nil); c != 400 {
			t.Fatalf("identity claim accepted: %d %s", c, bad)
		}
	}
	if c, _ := post("valid", submitTestJSON, map[string]string{"Auriga-Tenant-Id": "other"}); c != 400 {
		t.Fatal("identity header accepted")
	}
	if c, _ := post("invalid", submitTestJSON, nil); c != 401 {
		t.Fatal("invalid bearer accepted")
	}
	for _, mutate := range []func(){
		func() { b.Device = "other" }, func() { b.Tenant = "other" }, func() { b.Epoch = 0 }, func() { b.Revoked = true }, func() { b.NotAfter = time.Now().Add(-time.Second) },
	} {
		original := b
		mutate()
		r.bindings[b.Ingress] = b
		if c, _ := post("valid", submitTestJSON, nil); c != 401 {
			t.Fatalf("invalid binding accepted: %d", c)
		}
		b = original
		r.bindings[b.Ingress] = b
	}
	other := b
	other.Ingress = ingressForTicket("duplicate")
	r.bindings[other.Ingress] = other
	if c, _ := post("valid", submitTestJSON, nil); c != 409 {
		t.Fatal("ambiguous device connection accepted")
	}
	if len(f.calls) != 1 {
		t.Fatalf("rejected request reached collab: %v", f.calls)
	}
}
