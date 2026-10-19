package main

import (
	"context"
	"sync"
	"time"
)

// SessionConnHub cancels in-flight long-lived requests when a session is revoked or expires.
type SessionConnHub struct {
	mu   sync.Mutex
	seq  uint64
	byID map[string]map[uint64]context.CancelFunc
}

func NewSessionConnHub() *SessionConnHub {
	return &SessionConnHub{byID: map[string]map[uint64]context.CancelFunc{}}
}

// Attach binds a request context to sessionID until deadline (if non-zero) or CancelSession.
func (h *SessionConnHub) Attach(parent context.Context, sessionID string, deadline time.Time) (context.Context, context.CancelFunc) {
	if h == nil || sessionID == "" {
		ctx, cancel := context.WithCancel(parent)
		return ctx, cancel
	}
	ctx, cancel := context.WithCancel(parent)
	if !deadline.IsZero() {
		dctx, dcancel := context.WithDeadline(ctx, deadline)
		ctx = dctx
		prev := cancel
		cancel = func() {
			dcancel()
			prev()
		}
	}

	h.mu.Lock()
	h.seq++
	id := h.seq
	if h.byID[sessionID] == nil {
		h.byID[sessionID] = map[uint64]context.CancelFunc{}
	}
	h.byID[sessionID][id] = cancel
	h.mu.Unlock()

	var once sync.Once
	return ctx, func() {
		once.Do(func() {
			h.mu.Lock()
			if m := h.byID[sessionID]; m != nil {
				delete(m, id)
				if len(m) == 0 {
					delete(h.byID, sessionID)
				}
			}
			h.mu.Unlock()
			cancel()
		})
	}
}

// CancelSession aborts all attached connections for sessionID.
func (h *SessionConnHub) CancelSession(sessionID string) {
	if h == nil || sessionID == "" {
		return
	}
	h.mu.Lock()
	m := h.byID[sessionID]
	delete(h.byID, sessionID)
	h.mu.Unlock()
	for _, c := range m {
		c()
	}
}
