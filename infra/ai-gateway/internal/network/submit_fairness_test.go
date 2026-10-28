package network

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	collab "github.com/hansjone/auriga/infra/gen/auriga/collab/v1"
	iam "github.com/hansjone/auriga/infra/gen/auriga/iam/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type submitFairnessRPC struct {
	collab.NetworkMailboxServiceClient
	err error
}

func (f *submitFairnessRPC) Submit(_ context.Context, _ *collab.NetworkMailboxServiceSubmitRequest, _ ...grpc.CallOption) (*collab.NetworkMailboxServiceSubmitResponse, error) {
	return &collab.NetworkMailboxServiceSubmitResponse{ResultJson: []byte(`{"status":"platform_stored"}`)}, f.err
}

func newSubmitFairnessFixture(t *testing.T) *submitHandler {
	t.Helper()
	bindings := make(map[string]liveBinding)
	identities := make(map[string]*iam.VerifyDeviceAccessResponse)
	for tenant := 1; tenant <= 5; tenant++ {
		for device := 1; device <= 5; device++ {
			tid := fmt.Sprintf("00000000-0000-7000-8000-%012x", tenant)
			did := fmt.Sprintf("00000000-0000-7000-8000-%012x", device)
			token := fmt.Sprintf("%d/%d", tenant, device)
			b := liveBinding{Binding: Binding{Ingress: ingressForTicket(token), ServerID: "server", ClientID: 7, Account: clientAccount}, Tenant: tid, Device: did, Realm: "2", Node: "64", Epoch: 1, NotAfter: time.Now().Add(time.Minute)}
			bindings[b.Ingress] = b
			identities[token] = &iam.VerifyDeviceAccessResponse{Active: true, TenantId: tid, DeviceId: did, AccountId: testTenant, ExpiresAt: timestamppb.New(time.Now().Add(time.Minute))}
		}
	}
	verify := func(_ context.Context, token string) (*iam.VerifyDeviceAccessResponse, error) {
		return identities[token], nil
	}
	h := newSubmitHandler(&IAMRedeemer{bindings: bindings}, &MailboxClient{rpc: &submitFairnessRPC{}, timeout: time.Second}, verify)
	t.Cleanup(func() {
		h.admission.mu.Lock()
		defer h.admission.mu.Unlock()
		if h.admission.active != 0 || len(h.admission.tenants) != 0 || len(h.admission.devices) != 0 || len(h.verifySlots) != 0 {
			t.Errorf("admission leaked: active=%d tenants=%v devices=%v verification=%d", h.admission.active, h.admission.tenants, h.admission.devices, len(h.verifySlots))
		}
	})
	return h
}

func submitFairnessRequest(ctx context.Context, token string, body io.Reader) *http.Request {
	q := httptest.NewRequest(http.MethodPost, "https://example.com"+SubmitPath, body).WithContext(ctx)
	q.Header.Set("Content-Type", "application/json")
	q.Header.Set("Authorization", "Bearer "+token)
	return q
}

func checkSubmitOverloaded(t *testing.T, h *submitHandler, token string) {
	t.Helper()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, submitFairnessRequest(context.Background(), token, strings.NewReader(submitTestJSON)))
	var result struct {
		Code      string `json:"code"`
		Retryable bool   `json:"retryable"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if w.Code != http.StatusTooManyRequests || result.Code != "network.overloaded" || !result.Retryable {
		t.Fatalf("want retryable overload, got %d %s", w.Code, w.Body.String())
	}
}

type submitBlockedBody struct {
	ctx      context.Context
	started  chan struct{}
	release  chan struct{}
	contents io.Reader
	err      error
}

func (b *submitBlockedBody) Read(p []byte) (int, error) {
	if b.started != nil {
		close(b.started)
		b.started = nil
		select {
		case <-b.release:
		case <-b.ctx.Done():
			return 0, b.ctx.Err()
		}
	}
	if b.err != nil {
		return 0, b.err
	}
	return b.contents.Read(p)
}

func holdSubmit(t *testing.T, h *submitHandler, token, payload string, readErr error) (func(), func(), <-chan *httptest.ResponseRecorder) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	started, release := make(chan struct{}), make(chan struct{})
	body := &submitBlockedBody{ctx: ctx, started: started, release: release, contents: strings.NewReader(payload), err: readErr}
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, submitFairnessRequest(ctx, token, body))
		done <- w
		close(done)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("submit did not stop after cancellation")
		}
	})
	select {
	case <-started:
	case w := <-done:
		t.Fatalf("request rejected before body: %d %s", w.Code, w.Body.String())
	case <-time.After(time.Second):
		t.Fatal("request never reached body")
	}
	return func() { close(release) }, cancel, done
}

func TestSubmitTenantFairness(t *testing.T) {
	h := newSubmitFairnessFixture(t)
	for device := 1; device <= 4; device++ {
		for range 8 {
			holdSubmit(t, h, fmt.Sprintf("1/%d", device), submitTestJSON, nil)
		}
	}
	checkSubmitOverloaded(t, h, "1/5")
	// Even the same device ID in another tenant has an independent budget.
	w := httptest.NewRecorder()
	h.ServeHTTP(w, submitFairnessRequest(context.Background(), "2/1", strings.NewReader(submitTestJSON)))
	if w.Code != http.StatusOK {
		t.Fatalf("busy tenant blocked another tenant: %d %s", w.Code, w.Body.String())
	}
}

func TestSubmitDeviceFairness(t *testing.T) {
	h := newSubmitFairnessFixture(t)
	for range 8 {
		holdSubmit(t, h, "1/1", submitTestJSON, nil)
	}
	checkSubmitOverloaded(t, h, "1/1")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, submitFairnessRequest(context.Background(), "1/2", strings.NewReader(submitTestJSON)))
	if w.Code != http.StatusOK {
		t.Fatalf("busy device blocked another device: %d", w.Code)
	}
}

func TestSubmitGlobalLimit(t *testing.T) {
	h := newSubmitFairnessFixture(t)
	release, _, done := holdSubmit(t, h, "1/1", submitTestJSON, nil)
	for tenant := 1; tenant <= 4; tenant++ {
		for device := 1; device <= 4; device++ {
			for request := range 8 {
				if tenant == 1 && device == 1 && request == 0 {
					// The first request already occupies one global slot.
					continue
				}
				holdSubmit(t, h, fmt.Sprintf("%d/%d", tenant, device), submitTestJSON, nil)
			}
		}
	}
	checkSubmitOverloaded(t, h, "5/1")
	release()
	select {
	case w := <-done:
		if w.Code != http.StatusOK {
			t.Fatalf("released global slot: %d %s", w.Code, w.Body.String())
		}
	case <-time.After(time.Second):
		t.Fatal("global slot not released")
	}
	holdSubmit(t, h, "5/1", submitTestJSON, nil)
	checkSubmitOverloaded(t, h, "5/2")
}

func TestSubmitUnverifiedIdentityDoesNotUseAdmission(t *testing.T) {
	h := newSubmitFairnessFixture(t)
	for range 8 {
		holdSubmit(t, h, "1/1", submitTestJSON, nil)
	}
	verified := h.verify
	for _, tc := range []struct {
		name   string
		mutate func(*iam.VerifyDeviceAccessResponse)
	}{
		{name: "inactive", mutate: func(v *iam.VerifyDeviceAccessResponse) { v.Active = false }},
		{name: "invalid tenant", mutate: func(v *iam.VerifyDeviceAccessResponse) { v.TenantId = "other" }},
		{name: "expired", mutate: func(v *iam.VerifyDeviceAccessResponse) { v.ExpiresAt = timestamppb.New(time.Now().Add(-time.Minute)) }},
		{name: "no binding", mutate: func(v *iam.VerifyDeviceAccessResponse) { v.DeviceId = "00000000-0000-7000-8000-000000000099" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h.verify = func(ctx context.Context, token string) (*iam.VerifyDeviceAccessResponse, error) {
				v, err := verified(ctx, token)
				copyV := &iam.VerifyDeviceAccessResponse{Active: v.GetActive(), TenantId: v.GetTenantId(), DeviceId: v.GetDeviceId(), AccountId: v.GetAccountId(), ExpiresAt: v.GetExpiresAt()}
				tc.mutate(copyV)
				return copyV, err
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, submitFairnessRequest(context.Background(), "1/1", strings.NewReader(submitTestJSON)))
			if w.Code != http.StatusUnauthorized || !strings.Contains(w.Body.String(), `"code":"network.unauthorized"`) {
				t.Fatalf("want unauthorized before admission, got %d %s", w.Code, w.Body.String())
			}
		})
	}
	h.verify = verified
	checkSubmitOverloaded(t, h, "1/1")
}

func TestSubmitAdmissionRelease(t *testing.T) {
	for _, tc := range []struct {
		name, payload   string
		readErr, rpcErr error
		cancel          bool
		want            int
	}{
		{name: "success", payload: submitTestJSON, want: 200},
		{name: "invalid JSON", payload: "{", want: 400},
		{name: "body error", readErr: errors.New("body failed"), want: 400},
		{name: "RPC error", payload: submitTestJSON, rpcErr: status.Error(codes.Unavailable, "unavailable"), want: 502},
		{name: "canceled", payload: submitTestJSON, cancel: true, want: 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newSubmitFairnessFixture(t)
			h.client.rpc = &submitFairnessRPC{err: tc.rpcErr}
			release, cancel, done := holdSubmit(t, h, "1/1", tc.payload, tc.readErr)
			for range 7 {
				holdSubmit(t, h, "1/1", submitTestJSON, nil)
			}
			checkSubmitOverloaded(t, h, "1/1")
			if tc.cancel {
				cancel()
			} else {
				release()
			}
			select {
			case w := <-done:
				if w.Code != tc.want {
					t.Fatalf("want %d, got %d %s", tc.want, w.Code, w.Body.String())
				}
			case <-time.After(time.Second):
				t.Fatal("released request did not complete")
			}
			holdSubmit(t, h, "1/1", submitTestJSON, nil)
			checkSubmitOverloaded(t, h, "1/1")
		})
	}
}
