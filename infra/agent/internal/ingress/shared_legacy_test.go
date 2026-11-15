package ingress

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestDedicatedInstanceRequiresLegacyKeysWithSharedConfigured(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("independent instance reached upstream without legacy verification keys")
	}))
	defer upstream.Close()
	target, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	server := New(Config{
		Shared:   &SharedIngress{},
		Registry: NewMapRegistry(&Instance{ID: sharedTestID, Upstream: target}),
	})
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/i/"+sharedTestID+"/", nil)
	server.route(response, request)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", response.Code)
	}
}
