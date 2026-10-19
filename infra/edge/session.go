package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"
)

// Session mirrors contracts/kv/session.schema.json (consumer-side: allow extra fields).
type Session struct {
	SessionID            string    `json:"sessionId"`
	AccountID            string    `json:"accountId"`
	TenantID             string    `json:"tenantId"`
	Audience             string    `json:"audience"` // portal | admin
	AccessToken          string    `json:"accessToken"`
	AccessTokenExpiresAt time.Time `json:"accessTokenExpiresAt"`
	ExpiresAt            time.Time `json:"expiresAt"`
	IdleExpiresAt        time.Time `json:"idleExpiresAt"`
	MFA                  bool      `json:"mfa"`
	AuthTime             time.Time `json:"authTime"`
	StepUpAt             time.Time `json:"stepUpAt"`
	CSRFHash             string    `json:"csrfHash"`
	Revoked              bool      `json:"-"`
}

// SessionStore looks up sessions by SHA-256 hex of the raw cookie value.
type SessionStore interface {
	Get(ctx context.Context, cookieHash string) (*Session, error)
}

// SessionIntrospector is reserved for iam SessionService.IntrospectSession when KV misses.
// Default implementations return not found; never fabricate success.
type SessionIntrospector interface {
	Introspect(ctx context.Context, cookieHash string) (*Session, error)
}

// FailIntrospector always reports a miss.
type FailIntrospector struct{}

func (FailIntrospector) Introspect(context.Context, string) (*Session, error) {
	return nil, ErrSessionNotFound
}

var ErrSessionNotFound = fmt.Errorf("session not found")

// MemorySessionStore is an in-memory store for tests and local fallback.
type MemorySessionStore struct {
	mu       sync.RWMutex
	byHash   map[string]*Session
	byID     map[string]*Session
	OnRevoke func(sessionID string) // optional; wired to SessionConnHub
}

func NewMemorySessionStore() *MemorySessionStore {
	return &MemorySessionStore{
		byHash: map[string]*Session{},
		byID:   map[string]*Session{},
	}
}

func (m *MemorySessionStore) Put(cookieRaw string, s *Session) {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := *s
	m.byHash[HashCookie(cookieRaw)] = &cp
	if s.SessionID != "" {
		m.byID[s.SessionID] = &cp
	}
}

func (m *MemorySessionStore) Delete(cookieRaw string) {
	m.mu.Lock()
	var sid string
	h := HashCookie(cookieRaw)
	if s := m.byHash[h]; s != nil {
		sid = s.SessionID
		delete(m.byID, s.SessionID)
	}
	delete(m.byHash, h)
	m.mu.Unlock()
	if sid != "" && m.OnRevoke != nil {
		m.OnRevoke(sid)
	}
}

// Revoke marks the session revoked (if present) and notifies OnRevoke.
func (m *MemorySessionStore) Revoke(sessionID string) {
	m.mu.Lock()
	if s := m.byID[sessionID]; s != nil {
		s.Revoked = true
	}
	m.mu.Unlock()
	if m.OnRevoke != nil {
		m.OnRevoke(sessionID)
	}
}

func (m *MemorySessionStore) Get(_ context.Context, cookieHash string) (*Session, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	s, ok := m.byHash[cookieHash]
	if !ok || s == nil {
		return nil, ErrSessionNotFound
	}
	cp := *s
	return &cp, nil
}

func (m *MemorySessionStore) GetBySessionID(_ context.Context, sessionID string) (*Session, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	s, ok := m.byID[sessionID]
	if !ok || s == nil {
		return nil, ErrSessionNotFound
	}
	cp := *s
	return &cp, nil
}

// SessionByID is an optional extension for instance cookie → portal session checks.
type SessionByID interface {
	GetBySessionID(ctx context.Context, sessionID string) (*Session, error)
}

func getSessionByID(store SessionStore, ctx context.Context, sessionID string) (*Session, error) {
	if s, ok := store.(SessionByID); ok {
		return s.GetBySessionID(ctx, sessionID)
	}
	return nil, ErrSessionNotFound
}

// CachingSessionStore tries primary (KV), then introspector on miss.
type CachingSessionStore struct {
	Primary    SessionStore
	Introspect SessionIntrospector
}

func (c CachingSessionStore) Get(ctx context.Context, cookieHash string) (*Session, error) {
	if c.Primary != nil {
		s, err := c.Primary.Get(ctx, cookieHash)
		if err == nil {
			return s, nil
		}
		if err != ErrSessionNotFound {
			return nil, err
		}
	}
	if c.Introspect != nil {
		return c.Introspect.Introspect(ctx, cookieHash)
	}
	return nil, ErrSessionNotFound
}

func (c CachingSessionStore) GetBySessionID(ctx context.Context, sessionID string) (*Session, error) {
	return getSessionByID(c.Primary, ctx, sessionID)
}

// SessionRevokeHook is implemented by stores that can notify when a session is revoked.
type SessionRevokeHook interface {
	SetOnRevoke(fn func(sessionID string))
}

func (m *MemorySessionStore) SetOnRevoke(fn func(sessionID string)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.OnRevoke = fn
}

// wireSessionRevoke attaches CancelSession to memory / nats stores even when wrapped.
func wireSessionRevoke(store SessionStore, fn func(sessionID string)) {
	if store == nil || fn == nil {
		return
	}
	switch s := store.(type) {
	case CachingSessionStore:
		wireSessionRevoke(s.Primary, fn)
	case *CachingSessionStore:
		wireSessionRevoke(s.Primary, fn)
	case SessionRevokeHook:
		s.SetOnRevoke(fn)
	}
}

func HashCookie(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

func (s *Session) Valid(now time.Time, wantAudience string) error {
	if s == nil {
		return ErrSessionNotFound
	}
	if s.Revoked {
		return fmt.Errorf("session revoked")
	}
	if wantAudience != "" && s.Audience != wantAudience {
		return fmt.Errorf("audience mismatch")
	}
	if !s.ExpiresAt.IsZero() && !now.Before(s.ExpiresAt) {
		return fmt.Errorf("session expired")
	}
	if !s.IdleExpiresAt.IsZero() && !now.Before(s.IdleExpiresAt) {
		return fmt.Errorf("session idle expired")
	}
	if s.AccessToken == "" {
		return fmt.Errorf("missing access token")
	}
	return nil
}

// DecodeSessionJSON parses a KV value; unknown fields are ignored.
func DecodeSessionJSON(raw []byte) (*Session, error) {
	var s Session
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, err
	}
	if s.SessionID == "" || s.Audience == "" {
		return nil, fmt.Errorf("invalid session record")
	}
	return &s, nil
}

func cookieName(secure bool, base string) string {
	if secure {
		return "__Host-" + base
	}
	return base
}

func sessionCookieBase(audience string) string {
	if audience == "admin" {
		return "auriga_admin_session"
	}
	return "auriga_session"
}

func csrfCookieBase() string { return "auriga_csrf" }

func instCookieBase() string { return "auriga_inst" }

func normalizeAudience(host, portalHost, adminHost string) (audience string, ok bool) {
	h := strings.ToLower(host)
	if h == strings.ToLower(portalHost) {
		return "portal", true
	}
	if h == strings.ToLower(adminHost) {
		return "admin", true
	}
	return "", false
}
