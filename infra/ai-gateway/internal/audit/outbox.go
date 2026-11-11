// Package audit writes the common audit.recorded contract through the AI service's own outbox.
package audit

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"time"

	"github.com/google/uuid"
	"github.com/hansjone/auriga/infra/ai-gateway/internal/identifiers"
	"github.com/hansjone/auriga/infra/ai-gateway/internal/observability"
	"github.com/hansjone/auriga/infra/ai-gateway/internal/store"
	"github.com/jackc/pgx/v5"
	"github.com/nats-io/nats.go"
)

const Subject = "auriga.audit.ai-gateway"

type metadata struct{ actor, ip string }
type metadataKey struct{}

// WithMetadata is called only after the internal hop's authentication succeeds.
// Browser-provided headers are replaced at edge, never copied to this hop.
func WithMetadata(ctx context.Context, actor, ip, parent string) context.Context {
	if !identifiers.ValidUUID(actor) {
		actor = ""
	}
	if parsed := net.ParseIP(ip); parsed != nil {
		ip = parsed.String()
	} else {
		ip = ""
	}
	return context.WithValue(observability.Continue(ctx, parent), metadataKey{}, metadata{actor, ip})
}

// Record accepts fixed action/target metadata only. No business document, name, prompt,
// invitation, request body or credential can be accidentally serialized here.
func Record(ctx context.Context, tx pgx.Tx, tenant, action, targetType, target string) error {
	id, err := uuid.NewV7()
	if err != nil {
		return err
	}
	m, _ := ctx.Value(metadataKey{}).(metadata)
	actor := map[string]any{"type": "system", "id": "svc:ai-gateway"}
	if m.actor != "" {
		actor["type"] = "user"
		actor["id"] = m.actor
	}
	if m.ip != "" {
		actor["ip"] = m.ip
	}
	parent := observability.Parent(ctx)
	if !observability.Valid(parent) {
		parent = observability.Parent(observability.Continue(ctx, ""))
	}
	envelope := map[string]any{"specversion": "1.0", "id": id.String(), "type": "audit.recorded", "source": "auriga/ai-gateway", "time": time.Now().UTC().Format(time.RFC3339Nano), "subject": target, "datacontenttype": "application/json", "dataversion": "1", "tenantid": tenant, "traceparent": parent,
		"data": map[string]any{"actor": actor, "action": action, "target": map[string]string{"type": targetType, "id": target}, "result": "success"}}
	raw, err := json.Marshal(envelope)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO ai.audit_outbox(tenant_id,id,envelope) VALUES($1,$2,$3)`, tenant, id.String(), raw)
	return err
}

type Publish func(context.Context, string, []byte) error

// ForwardOnce owns a delivery transaction, never the business transaction. A lost ACK or
// failed mark retains the same event ID: JetStream deduplicates it, as does audit's inbox.
func ForwardOnce(ctx context.Context, db *store.Store, tenant string, publish Publish) (bool, error) {
	found := false
	var deliveryErr error
	err := db.Transaction(ctx, tenant, func(tx pgx.Tx) error {
		var id string
		var raw []byte
		err := tx.QueryRow(ctx, `SELECT id::text,envelope FROM ai.audit_outbox WHERE tenant_id=$1 AND published_at IS NULL AND next_attempt_at<=now() ORDER BY id LIMIT 1 FOR UPDATE SKIP LOCKED`, tenant).Scan(&id, &raw)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		found = true
		deliveryErr = publish(ctx, id, raw)
		if deliveryErr != nil {
			_, err = tx.Exec(ctx, `UPDATE ai.audit_outbox SET attempts=attempts+1,next_attempt_at=now()+least(60,power(2,least(attempts,6)))*interval '1 second' WHERE tenant_id=$1 AND id=$2`, tenant, id)
		} else {
			_, err = tx.Exec(ctx, `UPDATE ai.audit_outbox SET published_at=now(),attempts=attempts+1 WHERE tenant_id=$1 AND id=$2`, tenant, id)
		}
		return err
	})
	if err != nil {
		return found, err
	}
	return found, deliveryErr
}

// Run reconnects while offline; committed audit records remain durable until a broker ACK.
// Stream creation stays with the combined platform's existing AURIGA_AUDIT bootstrap.
func Run(ctx context.Context, db *store.Store, tenant, url string) error {
	conn, err := nats.Connect(url, nats.RetryOnFailedConnect(true), nats.MaxReconnects(-1), nats.ReconnectWait(time.Second), nats.Timeout(3*time.Second))
	if err != nil {
		return errors.New("audit NATS connection configuration rejected")
	}
	defer conn.Close()
	js, err := conn.JetStream()
	if err != nil {
		return errors.New("audit JetStream unavailable")
	}
	publish := func(ctx context.Context, id string, raw []byte) error {
		deadline, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		message := &nats.Msg{Subject: Subject, Data: raw, Header: nats.Header{}}
		message.Header.Set(nats.MsgIdHdr, id)
		_, err := js.PublishMsg(message, nats.Context(deadline))
		return err
	}
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
		for i := 0; i < 32; i++ {
			found, err := ForwardOnce(ctx, db, tenant, publish)
			if err != nil {
				if ctx.Err() == nil {
					slog.Warn("Audit delivery deferred; durable record retained")
				}
				break
			}
			if !found {
				break
			}
		}
	}
}
