package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestB9_InstancePathAllowsDotDotInFilename(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "http://inst.example.com/files/a..b", nil)
	req.URL.Path = "/files/a..b"
	if !instancePathOK(req) {
		t.Fatal("a..b should be allowed")
	}
	req.URL.Path = "/files/../secret"
	if instancePathOK(req) {
		t.Fatal(".. segment should be rejected")
	}
	req.URL.Path = "/.."
	if instancePathOK(req) {
		t.Fatal("/.. should be rejected")
	}
}

func TestB9_OpenNonceCookieNameIsPerInstance(t *testing.T) {
	a := openNonceCookieName(false, "example.com", "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")
	b := openNonceCookieName(false, "example.com", "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb")
	if a == b {
		t.Fatalf("expected distinct nonce cookie names, got %q", a)
	}
	if a == openNonceCookieBase() || b == openNonceCookieBase() {
		t.Fatal("instance suffix required")
	}
}

func TestB9_ConfigRequiresPortalUnderNonceDomain(t *testing.T) {
	cfg, _, _, _, _, _ := testEnv(t)
	cfg.InstanceParentDomain = "i.example.com"
	cfg.PortalHost = "portal.other.test"
	if err := cfg.validate(); err == nil {
		t.Fatal("portal outside cookie Domain should fail")
	}
	cfg.PortalHost = "portal.example.com"
	if err := cfg.validate(); err != nil {
		t.Fatal(err)
	}
}

func TestB9_CachingStoreRevokeCancelsAttachedConn(t *testing.T) {
	mem := NewMemorySessionStore()
	wrapped := CachingSessionStore{Primary: mem, Introspect: FailIntrospector{}}
	hub := NewSessionConnHub()
	wireSessionRevoke(wrapped, hub.CancelSession)

	putPortalSession(mem, "cookie-raw", "csrf", "tok")
	ctx, stop := hub.Attach(context.Background(), "11111111-1111-1111-1111-111111111111", time.Time{})
	defer stop()

	mem.Revoke("11111111-1111-1111-1111-111111111111")
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("expected attached connection canceled after store revoke")
	}
}

func TestB9_ParseRevokedSessionIDs(t *testing.T) {
	raw := []byte(`{"type":"identity.session_revoked","data":{"accountId":"a","sessionIds":["s1","s2"],"reason":"logout","actorId":null}}`)
	ids, ok := parseRevokedSessionIDs(raw)
	if !ok || len(ids) != 2 || ids[0] != "s1" || ids[1] != "s2" {
		t.Fatalf("ok=%v ids=%v", ok, ids)
	}
	_, ok = parseRevokedSessionIDs([]byte(`{"type":"other.event","data":{"sessionIds":["s1"]}}`))
	if ok {
		t.Fatal("wrong type must be rejected")
	}
	_, ok = parseRevokedSessionIDs([]byte(`{"data":{"sessionIds":["s1"]}}`))
	if ok {
		t.Fatal("empty type must be rejected")
	}
}

func TestB9_RateLimitFullTableDoesNotScanEveryHit(t *testing.T) {
	l := NewTokenBucketLimiter(1000, 1000, "")
	l.now = func() time.Time { return time.Unix(0, 0) }
	// Fill to capacity with distinct keys.
	for i := 0; i < maxRateBuckets; i++ {
		if !l.Allow("k"+itoa(i), "/") {
			t.Fatalf("seed allow failed at %d", i)
		}
	}
	// Existing key must still succeed without requiring a full-table eviction path each time.
	if !l.Allow("k0", "/") {
		t.Fatal("existing key should be allowed when table is full")
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [16]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
