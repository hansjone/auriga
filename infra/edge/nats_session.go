package main

import (
	"context"
	"fmt"
	"os"
)

// NATSSessionStore is an optional adapter for NATS KV bucket auriga-sessions.
// Without nats.go wired (installer / later task), construction fails closed and
// callers should fall back to memory. Interface is reserved for B7 wiring.
type NATSSessionStore struct{}

func NewNATSSessionStore(url string) (*NATSSessionStore, error) {
	if url == "" {
		return nil, fmt.Errorf("nats url empty")
	}
	// Soft dependency: only attempt when explicitly enabled.
	if os.Getenv("AURIGA_EDGE_NATS_ENABLE") != "1" {
		return nil, fmt.Errorf("NATS session store disabled (set AURIGA_EDGE_NATS_ENABLE=1 when nats.go is wired); falling back")
	}
	return nil, fmt.Errorf("NATS KV adapter not bundled in this skeleton; use sessionStore=memory or wire nats.go (see docs/dev/edge.md)")
}

func (n *NATSSessionStore) Get(context.Context, string) (*Session, error) {
	return nil, ErrSessionNotFound
}
