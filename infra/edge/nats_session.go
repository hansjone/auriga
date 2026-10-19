package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

const (
	sessionsKVBucket  = "auriga-sessions"
	sessionRevokeSubj = "auriga.evt.identity.session_revoked"
	sessionRevokeDur  = "edge-session-revoke"
)

// NATSSessionStore reads the auriga-sessions KV bucket and listens for
// identity.session_revoked events to cancel long-lived connections.
type NATSSessionStore struct {
	mu       sync.RWMutex
	nc       *nats.Conn
	kv       jetstream.KeyValue
	log      *slog.Logger
	onRevoke func(sessionID string)
	cancel   context.CancelFunc
}

// NewNATSSessionStore connects to NATS and opens (or creates) the sessions KV.
// Revoke fan-out starts when the AURIGA_EVENTS stream already exists (created by iam).
func NewNATSSessionStore(url string, log *slog.Logger) (*NATSSessionStore, error) {
	if url == "" {
		return nil, fmt.Errorf("nats url empty")
	}
	if log == nil {
		log = slog.Default()
	}
	nc, err := nats.Connect(url, nats.Name("auriga-edge"), nats.MaxReconnects(-1))
	if err != nil {
		return nil, fmt.Errorf("nats connect: %w", err)
	}
	js, err := jetstream.New(nc)
	if err != nil {
		nc.Close()
		return nil, fmt.Errorf("jetstream: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	kv, err := js.KeyValue(ctx, sessionsKVBucket)
	if err != nil {
		kv, err = js.CreateKeyValue(ctx, jetstream.KeyValueConfig{
			Bucket: sessionsKVBucket,
			TTL:    7 * 24 * time.Hour,
		})
		if err != nil {
			nc.Close()
			return nil, fmt.Errorf("sessions kv: %w", err)
		}
	}
	store := &NATSSessionStore{nc: nc, kv: kv, log: log}
	runCtx, runCancel := context.WithCancel(context.Background())
	store.cancel = runCancel
	if err := store.startRevokeConsumer(runCtx, js); err != nil {
		log.Warn("session revoke consumer not started", "error", err.Error())
	}
	return store, nil
}

func (n *NATSSessionStore) SetOnRevoke(fn func(sessionID string)) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.onRevoke = fn
}

func (n *NATSSessionStore) Close() {
	if n.cancel != nil {
		n.cancel()
	}
	if n.nc != nil {
		n.nc.Close()
	}
}

func (n *NATSSessionStore) Get(ctx context.Context, cookieHash string) (*Session, error) {
	if n == nil || n.kv == nil || cookieHash == "" {
		return nil, ErrSessionNotFound
	}
	entry, err := n.kv.Get(ctx, cookieHash)
	if err != nil {
		return nil, ErrSessionNotFound
	}
	s, err := DecodeSessionJSON(entry.Value())
	if err != nil {
		return nil, ErrSessionNotFound
	}
	return s, nil
}

func (n *NATSSessionStore) startRevokeConsumer(ctx context.Context, js jetstream.JetStream) error {
	_, err := js.Stream(ctx, "AURIGA_EVENTS")
	if err != nil {
		return fmt.Errorf("AURIGA_EVENTS stream unavailable: %w", err)
	}
	cons, err := js.CreateOrUpdateConsumer(ctx, "AURIGA_EVENTS", jetstream.ConsumerConfig{
		Durable:       sessionRevokeDur,
		FilterSubject: sessionRevokeSubj,
		AckPolicy:     jetstream.AckExplicitPolicy,
		DeliverPolicy: jetstream.DeliverNewPolicy,
	})
	if err != nil {
		return fmt.Errorf("revoke consumer: %w", err)
	}
	_, err = cons.Consume(func(msg jetstream.Msg) {
		n.handleRevokeMessage(msg.Data())
		_ = msg.Ack()
	})
	if err != nil {
		return fmt.Errorf("revoke consume: %w", err)
	}
	n.log.Info("edge listening for session revokes", "subject", sessionRevokeSubj)
	return nil
}

func (n *NATSSessionStore) handleRevokeMessage(raw []byte) {
	ids := parseRevokedSessionIDs(raw)
	n.mu.RLock()
	fn := n.onRevoke
	n.mu.RUnlock()
	if fn == nil {
		return
	}
	for _, id := range ids {
		fn(id)
	}
}

// parseRevokedSessionIDs extracts sessionIds from a CloudEvents envelope for identity.session_revoked.
func parseRevokedSessionIDs(raw []byte) []string {
	var env struct {
		Type string `json:"type"`
		Data struct {
			SessionIDs []string `json:"sessionIds"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil
	}
	if env.Type != "" && !strings.Contains(env.Type, "session_revoked") {
		return nil
	}
	return env.Data.SessionIDs
}

// NotifyRevoke is used by tests to simulate a revoke callback.
func (n *NATSSessionStore) NotifyRevoke(sessionID string) {
	n.mu.RLock()
	fn := n.onRevoke
	n.mu.RUnlock()
	if fn != nil && sessionID != "" {
		fn(sessionID)
	}
}
