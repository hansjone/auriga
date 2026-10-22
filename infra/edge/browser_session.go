package main

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	iamv1 "github.com/hansjone/auriga/infra/gen/auriga/iam/v1"
)

const sessionTouchInterval = 5 * time.Minute
const sessionTouchCapacity = 8192

type sessionActivityKey struct{ tenant, audience, sid string }
type sessionActivityAttempt struct {
	until   time.Time
	done    chan struct{}
	err     error
	revoked bool
}

// Only attempt timing/results are retained. Cookies, JWTs and CSRF material
// remain on the request stack; neither renewal nor Touch creates a session cache.
type sessionActivity struct {
	mu       sync.Mutex
	attempts map[sessionActivityKey]*sessionActivityAttempt
	epoch    atomic.Uint64
}

func newSessionActivity() *sessionActivity {
	return &sessionActivity{attempts: make(map[sessionActivityKey]*sessionActivityAttempt)}
}

func (a *sessionActivity) revoke(sid string) {
	// Introspection may not know its sid until the response arrives. Its
	// global fence only rejects that request; it never poisons a Touch result.
	a.epoch.Add(1)
	a.mu.Lock()
	defer a.mu.Unlock()
	for key, attempt := range a.attempts {
		if key.sid == sid {
			attempt.revoked = true
		}
	}
}

func (a *sessionActivity) wasRevoked(attempt *sessionActivityAttempt) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return attempt.revoked
}

func sameSessionBinding(a, b *Session) bool {
	return a != nil && b != nil && a.SessionID == b.SessionID && a.AccountID == b.AccountID && a.TenantID == b.TenantID && a.Audience == b.Audience && a.ExpiresAt.Equal(b.ExpiresAt) && a.AuthTime.Equal(b.AuthTime)
}

func introspectedSession(response *iamv1.IntrospectSessionResponse, audience, tenant string, now time.Time) (*Session, error) {
	if response == nil {
		return nil, errIAMUnavailable
	}
	if !response.Active {
		if response.Session != nil || response.AccessToken != "" || response.AccessTokenExpiresAt != nil {
			return nil, errIAMUnavailable
		}
		return nil, errIAMUnauthenticated
	}
	s := response.Session
	if s == nil || s.SessionId == "" || s.AccountId == "" || s.TenantId != tenant || tenant == "" || s.Audience != sessionAudience(audience) || sessionAudience(audience) == iamv1.SessionAudience_SESSION_AUDIENCE_UNSPECIFIED || response.AccessToken == "" {
		return nil, errIAMUnavailable
	}
	if s.ExpiresAt == nil || s.IdleExpiresAt == nil || s.CreatedAt == nil || response.AccessTokenExpiresAt == nil || s.ExpiresAt.CheckValid() != nil || s.IdleExpiresAt.CheckValid() != nil || s.CreatedAt.CheckValid() != nil || response.AccessTokenExpiresAt.CheckValid() != nil || s.StepUpAt != nil && s.StepUpAt.CheckValid() != nil {
		return nil, errIAMUnavailable
	}
	result := &Session{SessionID: s.SessionId, AccountID: s.AccountId, TenantID: tenant, Audience: audience, AccessToken: response.AccessToken, AccessTokenExpiresAt: response.AccessTokenExpiresAt.AsTime(), ExpiresAt: s.ExpiresAt.AsTime(), IdleExpiresAt: s.IdleExpiresAt.AsTime(), AuthTime: s.CreatedAt.AsTime(), MFA: s.Mfa}
	if s.StepUpAt != nil {
		result.StepUpAt = s.StepUpAt.AsTime()
	}
	if result.Valid(now, audience) != nil {
		return nil, errIAMUnauthenticated
	}
	if !now.Before(result.AccessTokenExpiresAt) || result.IdleExpiresAt.After(result.ExpiresAt) || result.AuthTime.After(now) {
		return nil, errIAMUnavailable
	}
	return result, nil
}

// browserSession requires the raw cookie supplied by this request. Instance
// ticket/session-key lookups deliberately never enter this renewal path.
func (g *Gateway) browserSession(r *http.Request, raw, audience string, recoverRead bool) (*Session, error) {
	now := g.Now().UTC()
	original, err := g.Sessions.Get(r.Context(), HashCookie(raw))
	if err != nil && !errors.Is(err, ErrSessionNotFound) {
		return nil, errIAMUnavailable
	}
	if original != nil && (original.Valid(now, audience) != nil || g.Cfg.Access.Valid() && original.TenantID != g.Cfg.Access.TenantID()) {
		return nil, errIAMUnauthenticated
	}
	if original != nil && now.Add(time.Minute).Before(original.AccessTokenExpiresAt) {
		return original, nil
	}
	// An installation entry is the only tenant source for a KV miss. Legacy
	// hosts cannot select a tenant or invent a default RPC identity.
	if !g.Cfg.Access.Valid() || g.SessionRPC == nil {
		return nil, errIAMUnavailable
	}
	epoch := g.Activity.epoch.Load()
	response, err := g.SessionRPC.IntrospectSession(r.Context(), raw, audience, g.Cfg.Access.TenantID())
	if err != nil {
		return nil, errIAMUnavailable
	}
	fresh, err := introspectedSession(response, audience, g.Cfg.Access.TenantID(), g.Now().UTC())
	if err != nil {
		if errors.Is(err, errIAMUnauthenticated) && original != nil {
			g.Conns.CancelSession(original.SessionID)
		}
		return nil, err
	}
	if original != nil && !sameSessionBinding(original, fresh) {
		return nil, errIAMUnavailable
	}
	projection, err := g.Sessions.Get(r.Context(), HashCookie(raw))
	if errors.Is(err, ErrSessionNotFound) && original == nil && recoverRead && r.Method == http.MethodGet {
		// A validated read can rebuild the projection via the existing Touch
		// contract. A write missing its CSRF projection must never do this.
		fresh, err = g.touchBrowserSession(r.Context(), raw, fresh)
		if err != nil {
			return nil, err
		}
		projection, err = g.Sessions.Get(r.Context(), HashCookie(raw))
	}
	if err != nil || projection.Valid(g.Now().UTC(), audience) != nil || !sameSessionBinding(projection, fresh) || original != nil && original.CSRFHash != projection.CSRFHash || g.Activity.epoch.Load() != epoch {
		return nil, errIAMUnavailable
	}
	// Introspection has no CSRF field. Use only the matching primary KV's
	// original projection, including IP admin's intentionally empty hash.
	fresh.CSRFHash = projection.CSRFHash
	if (g.Cfg.Access.Mode() != "ip" || audience != "admin") && !validCSRFHash(fresh.CSRFHash) {
		return nil, errIAMUnavailable
	}
	return fresh, nil
}

func validCSRFHash(hash string) bool {
	if len(hash) != 64 {
		return false
	}
	for _, c := range hash {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// touchBrowserSession is called only after the request's authentication and
// applicable Origin/CSRF/ownership checks. A saturated map refuses new keys;
// it never evicts a key still inside its five-minute attempt window.
func (g *Gateway) touchBrowserSession(ctx context.Context, raw string, expected *Session) (*Session, error) {
	if g.SessionRPC == nil || !g.Cfg.Access.Valid() {
		return expected, nil // no renewal is possible without the trusted RPC
	}
	now := g.Now().UTC()
	a := g.Activity
	key := sessionActivityKey{expected.TenantID, expected.Audience, expected.SessionID}
	a.mu.Lock()
	previous := a.attempts[key]
	if previous != nil && (now.Before(previous.until) || previous.done != nil) {
		done := previous.done
		revoked := previous.revoked
		a.mu.Unlock()
		if revoked {
			return nil, errIAMUnauthenticated
		}
		if done != nil {
			select {
			case <-ctx.Done():
				return nil, errIAMUnavailable
			case <-done:
			}
		}
		a.mu.Lock()
		err := previous.err
		revoked = previous.revoked
		a.mu.Unlock()
		if revoked {
			return nil, errIAMUnauthenticated
		}
		if err == nil {
			projection, readErr := g.Sessions.Get(ctx, HashCookie(raw))
			if readErr != nil || projection.Valid(g.Now().UTC(), expected.Audience) != nil || !sameSessionBinding(expected, projection) {
				return nil, errIAMUnavailable
			}
		}
		if a.wasRevoked(previous) {
			return nil, errIAMUnauthenticated
		}
		return expected, err
	}
	for k, attempt := range a.attempts {
		if attempt.done == nil && !now.Before(attempt.until) {
			delete(a.attempts, k)
		}
	}
	if len(a.attempts) >= sessionTouchCapacity {
		a.mu.Unlock()
		return nil, errIAMUnavailable
	}
	attempt := &sessionActivityAttempt{until: now.Add(sessionTouchInterval), done: make(chan struct{})}
	a.attempts[key] = attempt
	a.mu.Unlock()
	var response *iamv1.TouchSessionResponse
	var err error
	if a.wasRevoked(attempt) {
		err = errIAMUnauthenticated
	} else {
		response, err = g.SessionRPC.TouchSession(ctx, raw, expected.Audience, expected.TenantID)
	}
	var idle time.Time
	if err != nil || response == nil {
		if !errors.Is(err, errIAMUnauthenticated) {
			err = errIAMUnavailable
		}
	} else if !response.Active {
		err = errIAMUnauthenticated
		g.Conns.CancelSession(expected.SessionID)
	} else if response.IdleExpiresAt == nil || response.IdleExpiresAt.CheckValid() != nil {
		err = errIAMUnavailable
	} else {
		idle = response.IdleExpiresAt.AsTime()
		if !g.Now().UTC().Before(idle) || idle.After(expected.ExpiresAt) || idle.Before(expected.IdleExpiresAt) {
			err = errIAMUnavailable
		}
	}
	rpcErr := err
	if err == nil {
		projection, readErr := g.Sessions.Get(ctx, HashCookie(raw))
		if readErr != nil || projection.Valid(g.Now().UTC(), expected.Audience) != nil || !sameSessionBinding(expected, projection) || expected.CSRFHash != "" && expected.CSRFHash != projection.CSRFHash {
			err = errIAMUnavailable
		}
	}
	a.mu.Lock()
	if attempt.revoked {
		err = errIAMUnauthenticated
	}
	// Projection visibility is checked per request. A valid Touch whose
	// asynchronous KV projection is not visible yet still consumes the
	// interval, but must not block recovery once that projection arrives.
	attempt.err = rpcErr
	close(attempt.done)
	attempt.done = nil
	a.mu.Unlock()
	if err != nil {
		return nil, err
	}
	result := *expected
	result.IdleExpiresAt = idle
	if a.wasRevoked(attempt) {
		return nil, errIAMUnauthenticated
	}
	return &result, nil
}
