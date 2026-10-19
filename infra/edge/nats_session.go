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
	sessionRevokeType = "identity.session_revoked"
)

// NATSSessionStore reads the auriga-sessions KV bucket and listens for
// identity.session_revoked events to cancel long-lived connections.
type NATSSessionStore struct {
	mu         sync.RWMutex
	nc         *nats.Conn
	kv         jetstream.KeyValue
	log        *slog.Logger
	onRevoke   func(sessionID string)
	cancel     context.CancelFunc
	revokeReady chan struct{}
}

// NewNATSSessionStore connects to NATS (URL may embed user:password) and opens the sessions KV.
// Optional credsFile is a NATS credentials file path.
func NewNATSSessionStore(url string, credsFile string, log *slog.Logger) (*NATSSessionStore, error) {
	if url == "" {
		return nil, fmt.Errorf("nats url empty")
	}
	if log == nil {
		log = slog.Default()
	}
	opts := []nats.Option{nats.Name("auriga-edge"), nats.MaxReconnects(-1)}
	if credsFile != "" {
		opts = append(opts, nats.UserCredentials(credsFile))
	}
	nc, err := nats.Connect(url, opts...)
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
	store := &NATSSessionStore{nc: nc, kv: kv, log: log, revokeReady: make(chan struct{})}
	runCtx, runCancel := context.WithCancel(context.Background())
	store.cancel = runCancel
	go store.ensureRevokeConsumer(runCtx, js)
	return store, nil
}

// WaitRevokeConsumer blocks until the revoke consumer is consuming (tests).
func (n *NATSSessionStore) WaitRevokeConsumer(timeout time.Duration) error {
	select {
	case <-n.revokeReady:
		return nil
	case <-time.After(timeout):
		return fmt.Errorf("revoke consumer not ready within %s", timeout)
	}
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

func (n *NATSSessionStore) Delete(ctx context.Context, cookieHash string) error {
	if n == nil || n.kv == nil || cookieHash == "" {
		return nil
	}
	return n.kv.Delete(ctx, cookieHash)
}

// ensureRevokeConsumer retries until AURIGA_EVENTS exists; does not give up after one warn.
func (n *NATSSessionStore) ensureRevokeConsumer(ctx context.Context, js jetstream.JetStream) {
	backoff := time.Second
	for {
		if err := ctx.Err(); err != nil {
			return
		}
		if err := n.startRevokeConsumer(ctx, js); err != nil {
			n.log.Warn("session revoke consumer not ready; retrying", "error", err.Error(), "backoff", backoff.String())
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			if backoff < 30*time.Second {
				backoff *= 2
			}
			continue
		}
		return
	}
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
		n.handleRevokeMessage(msg)
	})
	if err != nil {
		return fmt.Errorf("revoke consume: %w", err)
	}
	n.log.Info("edge listening for session revokes", "subject", sessionRevokeSubj)
	select {
	case <-n.revokeReady:
	default:
		close(n.revokeReady)
	}
	return nil
}

func (n *NATSSessionStore) handleRevokeMessage(msg jetstream.Msg) {
	n.mu.RLock()
	fn := n.onRevoke
	n.mu.RUnlock()
	if fn == nil {
		// Handler not wired yet — Nak so the message is redelivered.
		_ = msg.Nak()
		return
	}
	ids, ok := parseRevokedSessionIDs(msg.Data())
	if !ok {
		// Malformed / wrong type: Ack to drop poison.
		_ = msg.Ack()
		return
	}
	for _, id := range ids {
		fn(id)
	}
	_ = msg.Ack()
}

// parseRevokedSessionIDs extracts sessionIds from a CloudEvents envelope.
// ok is false when the payload is not a valid identity.session_revoked event.
func parseRevokedSessionIDs(raw []byte) ([]string, bool) {
	var env struct {
		Type string `json:"type"`
		Data struct {
			SessionIDs []string `json:"sessionIds"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, false
	}
	if env.Type != sessionRevokeType && !strings.HasSuffix(env.Type, "."+sessionRevokeType) {
		return nil, false
	}
	return env.Data.SessionIDs, true
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
