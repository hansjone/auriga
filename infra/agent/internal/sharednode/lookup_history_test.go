package sharednode

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestRelayLookupPreservesHistoricalNotFound(t *testing.T) {
	r, authority := fixture(t)
	for _, test := range []struct {
		name    string
		failure error
		want    string
	}{
		{"expired-session", status.Error(codes.NotFound, "common.not_found"), "common.not_found"},
		{"logged-out-session", status.Error(codes.NotFound, "fixture-private-ticket"), "common.not_found"},
		{"wrapped-not-found", fmt.Errorf("fixture-private-wrapper: %w", status.Error(codes.NotFound, "common.not_found")), "common.not_found"},
		{"authority-outage", status.Error(codes.Unavailable, "fixture-private-authority"), "common.unavailable"},
		{"unknown-not-found-text", status.Error(codes.Unknown, "common.not_found"), "common.unavailable"},
		{"unknown-private-message", status.Error(codes.Internal, "fixture-private-authentication"), "common.unavailable"},
		{"deadline", status.Error(codes.DeadlineExceeded, "fixture-private-timeout"), "common.unavailable"},
	} {
		t.Run(test.name, func(t *testing.T) {
			authority.lookupFailures.Store(testSession, test.failure)
			defer authority.lookupFailures.Delete(testSession)
			raw, _ := json.Marshal(map[string]string{"tenantId": testTenant, "physicalInstanceId": testPool, "sessionId": testSession})
			req := httptest.NewRequest("POST", "/_auriga/shared-rpc/lookup", strings.NewReader(string(raw)))
			req.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			r.ServeHTTP(response, req)
			var problem struct {
				Code string `json:"code"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &problem); err != nil {
				t.Fatal(err)
			}
			if response.Code != http.StatusServiceUnavailable || problem.Code != test.want {
				t.Fatalf("W status lost: HTTP%d %s", response.Code, response.Body.String())
			}
			if strings.Contains(response.Body.String(), "fixture-private") {
				t.Fatal("private RPC exception escaped")
			}
		})
	}
	// An absent historical member does not mutate the current member's binding.
	raw, _ := json.Marshal(map[string]string{"tenantId": testTenant, "physicalInstanceId": testPool, "sessionId": testSession})
	req := httptest.NewRequest("POST", "/_auriga/shared-rpc/lookup", strings.NewReader(string(raw)))
	req.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	r.ServeHTTP(response, req)
	if response.Code != 200 || !strings.Contains(response.Body.String(), testSession) {
		t.Fatal("current session no longer resolves", response.Body.String())
	}
}
