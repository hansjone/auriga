package ingress

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

type registrationAuthority struct {
	sharedNodeAuthority
	registered bool
	failure    error
}

func (a *registrationAuthority) IsShared(context.Context, string) (bool, error) {
	return a.registered, a.failure
}
func TestSharedRegistrationCannotFallBackToStaleLegacyProcess(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("stale owner process reached") }))
	defer up.Close()
	target, _ := url.Parse(up.URL)
	a := &registrationAuthority{registered: true}
	s := New(Config{Shared: &SharedIngress{Authority: a}, Registry: NewMapRegistry(&Instance{ID: sharedTestID, Upstream: target})})
	for _, path := range []string{"/i/" + sharedTestID + "/", "/files/" + sharedTestID + "/", "/logs/" + sharedTestID} {
		w := httptest.NewRecorder()
		s.route(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 503 && w.Code != 403 {
			t.Fatal("registration bypass", path, w.Code)
		}
	}
	a.registered = false
	a.failure = errors.New("authority outage")
	w := httptest.NewRecorder()
	s.route(w, httptest.NewRequest("GET", "/i/"+sharedTestID+"/", nil))
	if w.Code != 503 {
		t.Fatal("outage selected owner fallback", w.Code)
	}
}
