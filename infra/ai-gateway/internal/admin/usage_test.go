package admin

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestUsageRouteAndQuery(t *testing.T) {
	o, ok := route("GET", prefix+"usage")
	if !ok || o.name != "listTenantAiUsage" || o.permission != "ai.usage.admin" || o.write {
		t.Fatalf("usage route: %+v %v", o, ok)
	}
	for _, method := range []string{"HEAD", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"} {
		if _, ok := route(method, prefix+"usage"); ok {
			t.Fatalf("accepted %s", method)
		}
	}
	for _, query := range []string{"limit=", "cursor=", "limit=0", "limit=201", "limit=1.5", "limit=1&limit=2", "cursor=a&cursor=b", "accountId=" + testUUID, "tenant=" + testUUID, "from=2026-01-01", "cursor=" + strings.Repeat("a", 513), "cursor=bad", "x=%zz", "limit=1;cursor=x"} {
		if err := validateQuery(httptest.NewRequest("GET", prefix+"usage?"+query, nil), o); err != failure("common.validation_failed") {
			t.Errorf("query %q: %v", query, err)
		}
	}
}

func TestUsageCursor(t *testing.T) {
	s := &Server{cipher: testCipher(t)}
	at := time.Date(2026, 1, 1, 1, 2, 3, 456000, time.UTC)
	cursor := s.usageCursor(testUUID, at, testUUID)
	gotAt, gotID, err := s.parseUsageCursor(testUUID, cursor)
	if err != nil || !gotAt.Equal(at) || gotID != testUUID || len(cursor) > 512 {
		t.Fatalf("cursor round trip: %v %s %v", gotAt, gotID, err)
	}
	for _, bad := range []string{cursor + "a", "owner-cursor", strings.Repeat("a", 513)} {
		if _, _, err = s.parseUsageCursor(testUUID, bad); err != failure("common.validation_failed") {
			t.Fatalf("bad cursor accepted: %v", err)
		}
	}
	if _, _, err = s.parseUsageCursor("00000000-0000-7000-8000-000000000012", cursor); err != failure("common.validation_failed") {
		t.Fatal("cross-tenant cursor accepted")
	}
}

func TestUsageAuthorization(t *testing.T) {
	key, auth := signer(t)
	s := &Server{authenticate: auth}
	for _, tc := range []struct {
		field  string
		value  any
		status int
		code   string
	}{
		{"perms", []string{"ai.providers.read", "ai.providers.write", "ai.models.manage"}, 403, "common.forbidden"},
		{"ctx", "portal", 403, "common.forbidden"},
		{"ctx", "instance", 403, "common.forbidden"},
		{"aud", []string{"auriga.ai-gateway"}, 401, "common.unauthenticated"},
		{"ptyp", "device", 401, "common.unauthenticated"},
		{"exp", time.Now().Add(-time.Minute).Unix(), 401, "common.unauthenticated"},
	} {
		t.Run(tc.field+"/"+tc.code, func(t *testing.T) {
			claims := validClaims()
			claims["perms"] = []string{"ai.usage.admin"}
			claims[tc.field] = tc.value
			expect(t, request(s, "GET", prefix+"usage", sign(t, key, claims, "at+jwt"), "", "", ""), tc.status, tc.code)
		})
	}
}
