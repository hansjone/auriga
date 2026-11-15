package network

import (
	"context"
	"crypto/sha256"
	"sync"
	"time"

	"github.com/hansjone/auriga/infra/ai-gateway/internal/sharedauth"
)

// SharedTarget is a logical destination. Desktop routing uses the existing
// msgnet-v2 node/local identity, resolved and checked by SharedMessageAuthority;
// it is never synthesized from a physical pool's account or broadcast subject.
type SharedTarget struct{ TenantID, SessionID, TaskID, DesktopNode, DesktopLocal string }
type SharedMessageAuthority interface {
	// Authorize checks current sender AND recipient, existing msgnet-v2 explicit
	// grants/parent-child rules, desktop device/boot and current epoch. The returned
	// cloud binding is authoritative. Desktop targets return a zero binding.
	Authorize(context.Context, sharedauth.Binding, SharedTarget) (sharedauth.Binding, error)
	// Desktop source is taken from the authenticated v2 ingress, not JSON.
	AuthorizeDesktop(context.Context, NodeIdentity, string, SharedTarget) (sharedauth.Binding, error)
}
type SharedDelivery struct {
	Sender    sharedauth.Binding
	Target    SharedTarget
	Recipient sharedauth.Binding
	MessageID string
	Body      []byte
}

// SharedTransport must use one private logical destination over the existing
// msgnet-v2 physical connection, preserving its signed frames/ACL/receipts. An
// error is terminal here; callers must reconcile v2 receipts, not replay commands.
type SharedTransport interface {
	Deliver(context.Context, SharedDelivery) error
}
type sharedMessageKey struct {
	Sender, Recipient sharedauth.Binding
	Target            SharedTarget
	ID                string
	Desktop           NodeIdentity
	Local             string
}
type sharedPending struct {
	delivery  SharedDelivery
	claims    sharedauth.Claims
	deadline  time.Time
	digest    [32]byte
	attempted bool
}

// SharedBroker bounds both payload queues and dedup tombstones. It is an
// admission adapter, not a replacement for the durable msgnet-v2 mailbox. Restart
// drops pending admission work; it never restores/replays old side effects.
type SharedBroker struct {
	mu        sync.Mutex
	gate      *sharedauth.Gate
	authority SharedMessageAuthority
	transport SharedTransport
	now       func() time.Time
	max       int
	pending   map[sharedMessageKey]*sharedPending
	received  map[sharedMessageKey]*sharedPending
}

func NewSharedBroker(g *sharedauth.Gate, a SharedMessageAuthority, t SharedTransport, now func() time.Time, max int) (*SharedBroker, error) {
	if g == nil || a == nil || t == nil || now == nil || max < 1 || max > 10000 {
		return nil, sharedauth.ErrInvalid
	}
	return &SharedBroker{gate: g, authority: a, transport: t, now: now, max: max, pending: map[sharedMessageKey]*sharedPending{}, received: map[sharedMessageKey]*sharedPending{}}, nil
}
func targetValid(t SharedTarget) bool {
	cloud := sharedauth.UUID(t.SessionID) && sharedauth.UUID(t.TaskID) && t.DesktopNode == "" && t.DesktopLocal == ""
	desktop := t.SessionID == "" && t.TaskID == "" && t.DesktopNode != "" && len(t.DesktopNode) <= 128 && t.DesktopLocal != "" && len(t.DesktopLocal) <= 128
	return sharedauth.UUID(t.TenantID) && (cloud || desktop)
}
func (b *SharedBroker) Enqueue(ctx context.Context, token, tenant, session, task, id string, target SharedTarget, body []byte) error {
	if !sharedauth.UUID(id) || !targetValid(target) || target.TenantID != tenant || len(body) == 0 || len(body) > 65536 {
		return sharedauth.ErrInvalid
	}
	c, e := b.gate.Verify(ctx, token, tenant, session, task, sharedauth.Message, "message.send")
	if e != nil {
		return e
	}
	recipient, e := b.authority.Authorize(ctx, c.Binding, target)
	if e != nil {
		return e
	}
	if target.SessionID != "" && (!recipient.Valid() || recipient.TenantID != tenant || recipient.SessionID != target.SessionID) {
		return sharedauth.ErrBinding
	}
	k := sharedMessageKey{Sender: c.Binding, Recipient: recipient, Target: target, ID: id}
	digest := sha256.Sum256(body)
	b.mu.Lock()
	defer b.mu.Unlock()
	for k, p := range b.pending {
		if !b.now().Before(p.deadline) {
			delete(b.pending, k)
		}
	}
	if old := b.pending[k]; old != nil {
		if old.digest != digest {
			return sharedauth.ErrBinding
		}
		if old.attempted {
			return &AppError{Code: CodeOutcomeUnknown, Scope: "request"}
		}
		return nil
	}
	if len(b.pending) >= b.max {
		return &AppError{Code: "network.overloaded", Retryable: true}
	}
	b.pending[k] = &sharedPending{delivery: SharedDelivery{c.Binding, target, recipient, id, append([]byte(nil), body...)}, claims: c, deadline: time.Unix(c.ExpiresAt, 0), digest: digest}
	return nil
}

// Flush is called by the authenticated pool/desktop adapter, not a browser.
// Every item is reauthorized immediately before one transport attempt. Failed
// commands remain tombstones and are never retried automatically.
func (b *SharedBroker) Flush(ctx context.Context) error {
	b.mu.Lock()
	work := []sharedPending{}
	for k, p := range b.pending {
		if !b.now().Before(p.deadline) {
			delete(b.pending, k)
			continue
		}
		if !p.attempted {
			p.attempted = true
			work = append(work, *p)
			p.delivery.Body = nil
		}
	}
	b.mu.Unlock()
	var first error
	for _, p := range work {
		e := b.gate.Recheck(ctx, p.claims, "message.send")
		if e == nil {
			var current sharedauth.Binding
			current, e = b.authority.Authorize(ctx, p.delivery.Sender, p.delivery.Target)
			if e == nil && current != p.delivery.Recipient {
				e = sharedauth.ErrBinding
			}
		}
		if e == nil {
			run, cancel := context.WithDeadline(ctx, p.deadline)
			e = b.transport.Deliver(run, p.delivery)
			cancel()
		}
		if first == nil {
			first = e
		}
	}
	return first
}

// Receive is the cloud ingress boundary; a physical subscription alone confers
// no logical mailbox authority. Desktop receive stays on msgnet-v2 device ACL.
func (b *SharedBroker) Receive(ctx context.Context, token, tenant, session, task string, d SharedDelivery, consume func(context.Context, []byte) error) error {
	return b.receive(ctx, token, tenant, session, task, d, NodeIdentity{}, "", consume)
}

// ReceiveDesktop accepts only authenticated msgnet-v2 ingress identity. Binding
// and node/local authorization are rechecked by the authority before execution.
func (b *SharedBroker) ReceiveDesktop(ctx context.Context, source NodeIdentity, local, token, tenant, session, task string, d SharedDelivery, consume func(context.Context, []byte) error) error {
	if source.TenantID != tenant || source.NodeID == "" || source.Incarnation == "" || source.Epoch == 0 || local == "" || d.Sender != (sharedauth.Binding{}) {
		return sharedauth.ErrInvalid
	}
	return b.receive(ctx, token, tenant, session, task, d, source, local, consume)
}
func (b *SharedBroker) receive(ctx context.Context, token, tenant, session, task string, d SharedDelivery, desktop NodeIdentity, local string, consume func(context.Context, []byte) error) error {
	if consume == nil || !sharedauth.UUID(d.MessageID) || len(d.Body) == 0 || len(d.Body) > 65536 || !targetValid(d.Target) {
		return sharedauth.ErrInvalid
	}
	c, e := b.gate.Verify(ctx, token, tenant, session, task, sharedauth.Message, "message.receive")
	if e != nil {
		return e
	}
	if c.Binding != d.Recipient || d.Target.TenantID != tenant || d.Target.SessionID != session || d.Target.TaskID != task {
		return sharedauth.ErrBinding
	}
	var current sharedauth.Binding
	if desktop.NodeID != "" {
		current, e = b.authority.AuthorizeDesktop(ctx, desktop, local, d.Target)
	} else {
		if !d.Sender.Valid() {
			return sharedauth.ErrInvalid
		}
		current, e = b.authority.Authorize(ctx, d.Sender, d.Target)
	}
	if e != nil {
		return e
	}
	if current != c.Binding {
		return sharedauth.ErrBinding
	}
	// Reserve before executing; ambiguous failures are terminal. Durable replay
	// protection beyond this bounded admission window belongs to v2 receipts.
	k := sharedMessageKey{Sender: d.Sender, Recipient: d.Recipient, Target: d.Target, ID: d.MessageID, Desktop: desktop, Local: local}
	digest := sha256.Sum256(d.Body)
	b.mu.Lock()
	for k, p := range b.received {
		if !b.now().Before(p.deadline) {
			delete(b.received, k)
		}
	}
	if old := b.received[k]; old != nil {
		b.mu.Unlock()
		if old.digest != digest {
			return sharedauth.ErrBinding
		}
		return &AppError{Code: CodeOutcomeUnknown, Scope: "recipient"}
	}
	if len(b.received) >= b.max {
		b.mu.Unlock()
		return &AppError{Code: "network.overloaded", Retryable: true}
	}
	b.received[k] = &sharedPending{digest: digest, deadline: time.Unix(c.ExpiresAt, 0)}
	b.mu.Unlock()
	run, cancel := context.WithDeadline(ctx, time.Unix(c.ExpiresAt, 0))
	defer cancel()
	return consume(run, append([]byte(nil), d.Body...))
}
