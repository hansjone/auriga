package network

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	collab "github.com/hansjone/auriga/infra/gen/auriga/collab/v1"
	iam "github.com/hansjone/auriga/infra/gen/auriga/iam/v1"
	"google.golang.org/grpc"
)

type submitReleaseRPC struct {
	collab.NetworkMailboxServiceClient
	submit func(context.Context) (*collab.NetworkMailboxServiceSubmitResponse, error)
}

func (f *submitReleaseRPC) Submit(ctx context.Context, _ *collab.NetworkMailboxServiceSubmitRequest, _ ...grpc.CallOption) (*collab.NetworkMailboxServiceSubmitResponse, error) {
	return f.submit(ctx)
}

func checkSubmitReleased(t *testing.T, h *submitHandler) {
	t.Helper()
	h.admission.mu.Lock()
	defer h.admission.mu.Unlock()
	if len(h.verifySlots) != 0 || h.admission.active != 0 || len(h.admission.tenants) != 0 || len(h.admission.devices) != 0 {
		t.Fatalf("slots leaked: verification=%d active=%d tenants=%v devices=%v", len(h.verifySlots), h.admission.active, h.admission.tenants, h.admission.devices)
	}
}

func checkSubmitReadmitted(t *testing.T, h *submitHandler) {
	t.Helper()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, submitFairnessRequest(t.Context(), "1/1", strings.NewReader(submitTestJSON)))
	if w.Code != http.StatusOK {
		t.Fatalf("request after release rejected: %d %s", w.Code, w.Body.String())
	}
	checkSubmitReleased(t, h)
}

func TestSubmitPanicRelease(t *testing.T) {
	for _, stage := range []string{"verification", "submission"} {
		t.Run(stage, func(t *testing.T) {
			h := newSubmitFairnessFixture(t)
			verify, rpc := h.verify, h.client.rpc
			const panicValue = "submit RPC panic"
			attempts := submitDeviceLimit + 1
			if stage == "verification" {
				attempts = cap(h.verifySlots) + 1
				h.verify = func(context.Context, string) (*iam.VerifyDeviceAccessResponse, error) {
					panic(panicValue)
				}
			} else {
				h.client.rpc = &submitReleaseRPC{submit: func(context.Context) (*collab.NetworkMailboxServiceSubmitResponse, error) {
					panic(panicValue)
				}}
			}
			for i := range attempts {
				// Recover at the request boundary, as net/http does. The handler
				// must release its slots without swallowing or changing the panic.
				func() {
					defer func() {
						if got := recover(); got != panicValue {
							t.Fatalf("attempt %d: want original panic, got %v", i, got)
						}
					}()
					h.ServeHTTP(httptest.NewRecorder(), submitFairnessRequest(t.Context(), "1/1", strings.NewReader(submitTestJSON)))
				}()
				checkSubmitReleased(t, h)
			}
			h.verify, h.client.rpc = verify, rpc
			checkSubmitReadmitted(t, h)
		})
	}
}

func TestSubmitRPCContextRelease(t *testing.T) {
	for _, stage := range []string{"verification", "submission"} {
		for _, cause := range []string{"cancellation", "timeout"} {
			t.Run(stage+"/"+cause, func(t *testing.T) {
				synctest.Test(t, func(t *testing.T) {
					h := newSubmitFairnessFixture(t)
					verify, rpc := h.verify, h.client.rpc
					ctx, cancel := context.WithCancel(t.Context())
					defer cancel()
					entered := make(chan context.Context, 1)
					if stage == "verification" {
						h.verify = func(ctx context.Context, _ string) (*iam.VerifyDeviceAccessResponse, error) {
							entered <- ctx
							<-ctx.Done()
							return nil, ctx.Err()
						}
					} else {
						h.client.rpc = &submitReleaseRPC{submit: func(ctx context.Context) (*collab.NetworkMailboxServiceSubmitResponse, error) {
							entered <- ctx
							<-ctx.Done()
							return nil, ctx.Err()
						}}
					}
					started := time.Now()
					done := make(chan *httptest.ResponseRecorder, 1)
					go func() {
						w := httptest.NewRecorder()
						h.ServeHTTP(w, submitFairnessRequest(ctx, "1/1", strings.NewReader(submitTestJSON)))
						done <- w
					}()
					rpcContext := <-entered
					synctest.Wait()
					wantVerify, wantActive := 1, 0
					if stage == "submission" {
						wantVerify, wantActive = 0, 1
					}
					h.admission.mu.Lock()
					active, tenants, devices := h.admission.active, len(h.admission.tenants), len(h.admission.devices)
					h.admission.mu.Unlock()
					if len(h.verifySlots) != wantVerify || active != wantActive || tenants != wantActive || devices != wantActive {
						t.Fatalf("wrong slots while RPC waits: verification=%d active=%d tenants=%d devices=%d", len(h.verifySlots), active, tenants, devices)
					}
					deadline, ok := rpcContext.Deadline()
					wantDeadline := 10 * time.Second
					if stage == "submission" {
						wantDeadline = h.client.timeout
					}
					if !ok || deadline.Sub(started) != wantDeadline || rpcContext.Err() != nil {
						t.Fatal("RPC did not start with the original bounded live deadline")
					}
					wantErr := context.DeadlineExceeded
					if cause == "cancellation" {
						wantErr = context.Canceled
						cancel()
					}
					// All goroutines now block on the RPC context. In the timeout
					// case virtual time advances to the unchanged production deadline.
					w := <-done
					if rpcContext.Err() != wantErr {
						t.Fatalf("want RPC context error %v, got %v", wantErr, rpcContext.Err())
					}
					if cause == "timeout" && time.Since(started) != wantDeadline {
						t.Fatalf("request did not stop at its deadline: %v", time.Since(started))
					}
					var result struct {
						Code      string `json:"code"`
						Retryable bool   `json:"retryable"`
					}
					if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
						t.Fatal(err)
					}
					wantStatus, wantCode := http.StatusUnauthorized, "network.unauthorized"
					if stage == "submission" {
						wantStatus, wantCode = http.StatusBadGateway, "network.overloaded"
					}
					if w.Code != wantStatus || result.Code != wantCode || result.Retryable != (stage == "submission") {
						t.Fatalf("want %d %s, got %d %s", wantStatus, wantCode, w.Code, w.Body.String())
					}
					checkSubmitReleased(t, h)
					h.verify, h.client.rpc = verify, rpc
					checkSubmitReadmitted(t, h)
				})
			})
		}
	}
}
