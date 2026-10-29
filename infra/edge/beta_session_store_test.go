package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	iamv1 "github.com/hansjone/auriga/infra/gen/auriga/iam/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestBetaMemorySessionProjectionReproducesLogin503(t *testing.T) {
	g, _, memory := accessFixture(t, false, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("missing authoritative projection reached upstream")
		w.WriteHeader(204)
	}))
	backend, _ := browserRPCFixture(t, g, memory)
	putBrowserSession(g, backend, "private-browser-cookie", "admin")
	memory.Delete("private-browser-cookie")
	backend.project = false // IAM projects to PostgreSQL, not this unrelated edge memory.
	var logs bytes.Buffer
	g.Log = slog.New(slog.NewJSONHandler(&logs, nil))
	w := browserRequest(g, "admin", "GET", "/api/iam/v1/me", "private-browser-cookie")
	if w.Code != 503 || backend.introspects != 1 || backend.touches != 1 {
		t.Fatalf("expected projection failure after successful RPCs: status=%d introspects=%d touches=%d", w.Code, backend.introspects, backend.touches)
	}
	event := singleBetaWarning(t, logs.Bytes())
	requestID, requestIDOK := event["requestId"].(string)
	if event["level"] != "WARN" || event["reason"] != "browser_session_unavailable" || !requestIDOK || requestID == "" {
		t.Fatalf("missing rejection category/request ID: %v", event)
	}
	if strings.Contains(logs.String(), "private-browser-cookie") {
		t.Fatal("browser cookie leaked into rejection log")
	}
}

const betaPrivateMarker = "RAW_PRIVATE_CREDENTIAL"

type betaLogRPC struct{ touch func() }

func (s betaLogRPC) IntrospectSession(context.Context, string, string, string) (*iamv1.IntrospectSessionResponse, error) {
	return nil, errors.New(betaPrivateMarker)
}
func (s betaLogRPC) TouchSession(context.Context, string, string, string) (*iamv1.TouchSessionResponse, error) {
	if s.touch == nil {
		return nil, errors.New(betaPrivateMarker)
	}
	s.touch()
	return &iamv1.TouchSessionResponse{Active: true, IdleExpiresAt: timestamppb.New(time.Now().UTC().Add(35 * time.Minute))}, nil
}

type betaLogCSRF struct{}

func (betaLogCSRF) Verify(context.Context, string, string, *Session, time.Time) error {
	return errors.New(betaPrivateMarker)
}

type betaLogTransport struct{}

func (betaLogTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New(betaPrivateMarker)
}

func TestBeta503CategoriesDoNotLogCredentials(t *testing.T) {
	for _, reason := range []string{"admin_csrf_verifier_unavailable", "admin_csrf_rpc_unavailable", "iam_transport_unavailable", "api_transport_unavailable", "browser_session_touch_unavailable", "browser_access_token_expired", "upstream_transport_unavailable"} {
		t.Run(reason, func(t *testing.T) {
			g, _, store := accessFixture(t, false, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				t.Error("rejected request reached backend")
				w.WriteHeader(204)
			}))
			now := time.Now().UTC()
			g.Now = func() time.Time { return now }
			session := putPortalSession(store, betaPrivateMarker, "csrf", betaPrivateMarker)
			session.TenantID, session.Audience = g.Cfg.Access.TenantID(), "admin"
			session.IdleExpiresAt = now.Add(10 * time.Minute)
			session.AccessTokenExpiresAt = now.Add(2 * time.Minute)
			store.Put(betaPrivateMarker, session)
			method, path := "GET", "/api/org/v1/me"
			switch reason {
			case "admin_csrf_verifier_unavailable":
				method, path = "POST", "/api/org/v1/admin/members"
			case "admin_csrf_rpc_unavailable":
				method, path = "POST", "/api/org/v1/admin/members"
				g.AdminCSRF = betaLogCSRF{}
			case "iam_transport_unavailable":
				path = "/api/iam/v1/me"
				g.IAMTransport = nil
			case "api_transport_unavailable":
				g.APITransports[g.orgURL] = nil
			case "browser_session_touch_unavailable":
				g.SessionRPC = betaLogRPC{}
			case "browser_access_token_expired":
				g.SessionRPC = betaLogRPC{touch: func() { now = now.Add(3 * time.Minute) }}
			case "upstream_transport_unavailable":
				g.APITransports[g.orgURL] = betaLogTransport{}
			}
			var logs bytes.Buffer
			g.Log = slog.New(slog.NewJSONHandler(&logs, nil))
			response := browserRequest(g, "admin", method, path, betaPrivateMarker)
			if response.Code != 503 {
				t.Fatalf("expected 503, got %d", response.Code)
			}
			event := singleBetaWarning(t, logs.Bytes())
			if event["level"] != "WARN" || event["reason"] != reason || event["requestId"] == nil {
				t.Fatalf("wrong rejection category: %v", event)
			}
			if strings.Contains(logs.String(), betaPrivateMarker) {
				t.Fatal("private request or dependency error leaked")
			}
		})
	}
}

func singleBetaWarning(t *testing.T, lines []byte) map[string]any {
	t.Helper()
	var warnings []map[string]any
	for _, line := range bytes.Split(bytes.TrimSpace(lines), []byte("\n")) {
		var event map[string]any
		if err := json.Unmarshal(line, &event); err != nil {
			t.Fatal(err)
		}
		if event["level"] == "WARN" {
			warnings = append(warnings, event)
		}
	}
	if len(warnings) != 1 {
		t.Fatalf("expected one structured WARN, got %d", len(warnings))
	}
	return warnings[0]
}
