package network

import (
	"context"
	"encoding/json"
	"time"

	"github.com/hansjone/auriga/infra/ai-gateway/internal/sharedauth"
)

// SharedV2Endpoint is a registered logical mailbox on an authenticated physical
// connection. W/P supplies the session-to-typed-local association. IAM/collab
// still verifies the source observation and final delivery authorization.
type SharedV2Endpoint struct {
	Binding                               sharedauth.Binding
	TenantID, Ingress, Realm, Node, Local string
	BootID, SourceBinding, ProducerEpoch  string
	ConnectionEpoch                       uint64
}

// SharedV2Directory reads current registered associations and explicit grants.
// It must check task liveness and placement epoch on every resolution. It never
// allocates a local or grants receive permission as a side effect of a lookup.
// This W/P port remains a K-compatible double until joint wiring is available.
type SharedV2Directory interface {
	Cloud(context.Context, sharedauth.Binding) (SharedV2Endpoint, error)
	Target(context.Context, SharedTarget) (SharedV2Endpoint, error)
	Desktop(context.Context, NodeIdentity, string) (SharedV2Endpoint, error)
	CheckAccess(context.Context, SharedV2Endpoint, SharedV2Endpoint) error
}

type SharedV2Adapter struct {
	directory SharedV2Directory
	redeemer  *IAMRedeemer
	mailbox   *MailboxClient
	router    *mailboxRouter
}

// SharedV2 uses the SAME live IAM registry and authenticated collab RPC client
// as the existing network Runtime. It does not open another NATS connection.
func (r *Runtime) SharedV2(directory SharedV2Directory) (*SharedV2Adapter, error) {
	if directory == nil || r.redeemer == nil || r.mailbox == nil || r.mailbox.client == nil {
		return nil, sharedauth.ErrInvalid
	}
	return &SharedV2Adapter{directory: directory, redeemer: r.redeemer, mailbox: r.mailbox.client, router: r.mailbox}, nil
}

// SharedMessages composes logical admission with this Runtime's v2 transport.
func (r *Runtime) SharedMessages(gate *sharedauth.Gate, directory SharedV2Directory, limit int) (*SharedBroker, error) {
	adapter, err := r.SharedV2(directory)
	if err != nil {
		return nil, err
	}
	return NewSharedBroker(gate, adapter, adapter, time.Now, limit)
}

func (a *SharedV2Adapter) connection(endpoint SharedV2Endpoint) (liveBinding, error) {
	if !sharedauth.UUID(endpoint.TenantID) || !ValidIngressID(endpoint.Ingress) || !validNetworkNumber(endpoint.Realm) || !validNetworkNumber(endpoint.Node) || !validNetworkNumber(endpoint.Local) || endpoint.Local == "0" || endpoint.ConnectionEpoch == 0 || endpoint.BootID == "" {
		return liveBinding{}, sharedauth.ErrBinding
	}
	a.redeemer.mu.Lock()
	live, ok := a.redeemer.bindings[endpoint.Ingress]
	boot := a.router.sourceBoot[endpoint.Ingress]
	a.redeemer.mu.Unlock()
	if !ok || live.Revoked || !time.Now().Before(live.NotAfter) || live.Tenant != endpoint.TenantID || live.Realm != endpoint.Realm || live.Node != endpoint.Node || live.Epoch != endpoint.ConnectionEpoch || boot != endpoint.BootID {
		return liveBinding{}, sharedauth.ErrRevoked
	}
	return live, nil
}

func (a *SharedV2Adapter) endpoints(ctx context.Context, source sharedauth.Binding, target SharedTarget) (SharedV2Endpoint, SharedV2Endpoint, error) {
	if !source.Valid() || !targetValid(target) || source.TenantID != target.TenantID {
		return SharedV2Endpoint{}, SharedV2Endpoint{}, sharedauth.ErrBinding
	}
	from, err := a.directory.Cloud(ctx, source)
	if err != nil {
		return from, SharedV2Endpoint{}, err
	}
	if from.Binding != source || from.TenantID != source.TenantID {
		return from, SharedV2Endpoint{}, sharedauth.ErrBinding
	}
	to, err := a.directory.Target(ctx, target)
	if err != nil {
		return from, to, err
	}
	if to.TenantID != source.TenantID {
		return from, to, sharedauth.ErrBinding
	}
	if target.SessionID != "" {
		if !to.Binding.Valid() || to.Binding.TenantID != target.TenantID || to.Binding.SessionID != target.SessionID {
			return from, to, sharedauth.ErrBinding
		}
	} else if to.Binding != (sharedauth.Binding{}) || to.Node != target.DesktopNode || to.Local != target.DesktopLocal {
		return from, to, sharedauth.ErrBinding
	}
	if _, err = a.connection(from); err != nil {
		return from, to, err
	}
	if _, err = a.connection(to); err != nil {
		return from, to, err
	}
	return from, to, a.directory.CheckAccess(ctx, from, to)
}

func (a *SharedV2Adapter) Authorize(ctx context.Context, source sharedauth.Binding, target SharedTarget) (sharedauth.Binding, error) {
	_, to, err := a.endpoints(ctx, source, target)
	return to.Binding, err
}

func (a *SharedV2Adapter) AuthorizeDesktop(ctx context.Context, source NodeIdentity, local string, target SharedTarget) (sharedauth.Binding, error) {
	from, err := a.directory.Desktop(ctx, source, local)
	if err != nil {
		return sharedauth.Binding{}, err
	}
	if source.TenantID != target.TenantID || from.TenantID != source.TenantID || from.Realm != source.Realm || from.Node != source.NodeID || from.Local != local || from.ConnectionEpoch != source.Epoch || from.Binding != (sharedauth.Binding{}) {
		return sharedauth.Binding{}, sharedauth.ErrBinding
	}
	live, err := a.connection(from)
	if err != nil {
		return sharedauth.Binding{}, err
	}
	if live.Incarnation != source.Incarnation {
		return sharedauth.Binding{}, sharedauth.ErrRevoked
	}
	to, err := a.directory.Target(ctx, target)
	if err != nil {
		return sharedauth.Binding{}, err
	}
	if !to.Binding.Valid() || to.Binding.TenantID != source.TenantID || to.Binding.SessionID != target.SessionID {
		return sharedauth.Binding{}, sharedauth.ErrBinding
	}
	if _, err = a.connection(to); err != nil {
		return sharedauth.Binding{}, err
	}
	return to.Binding, a.directory.CheckAccess(ctx, from, to)
}

// Submit sends one existing durable/atomic session unicast through collab.
// Result bytes are returned intact for the host's existing v2 result verifier.
// The saved source binding and producer epoch are never derived from the pool
// owner, connection epoch, message ID or caller-selected frame fields.
func (a *SharedV2Adapter) Submit(ctx context.Context, d SharedDelivery) ([]byte, error) {
	from, to, err := a.endpoints(ctx, d.Sender, d.Target)
	if err != nil {
		return nil, err
	}
	if to.Binding != d.Recipient || !sharedauth.UUID(d.MessageID) || !opaqueID.MatchString(from.SourceBinding) {
		return nil, sharedauth.ErrBinding
	}
	if _, err := parseP64("producer epoch", from.ProducerEpoch); err != nil {
		return nil, err
	}
	deadline, ok := ctx.Deadline()
	if !ok {
		return nil, sharedauth.ErrInvalid
	}
	ttl := int64(time.Until(deadline) / time.Second)
	if ttl < 1 {
		return nil, sharedauth.ErrExpired
	}
	if ttl > 600 {
		ttl = 600
	}
	if validateRegistrationJSON(d.Body) != nil {
		return nil, sharedauth.ErrInvalid
	}
	request := map[string]any{
		"kind": "message", "durability": "durable", "dispatch_mode": "atomic", "intent": "request", "ttl": ttl,
		"to":          map[string]any{"version": 1, "realm": to.Realm, "type": "session", "node": to.Node, "local": to.Local},
		"source":      map[string]string{"source_binding": from.SourceBinding},
		"idempotency": map[string]string{"producer_epoch": from.ProducerEpoch, "key": d.MessageID},
		"body":        json.RawMessage(d.Body),
	}
	raw, err := json.Marshal(request)
	if err != nil || !validSubmitJSON(raw) {
		return nil, sharedauth.ErrInvalid
	}
	live, err := a.connection(from)
	if err != nil {
		return nil, err
	}
	result, appErr := a.mailbox.Call(ctx, live, "submit", raw)
	if appErr != nil {
		return nil, appErr
	}
	return result, nil
}

func (a *SharedV2Adapter) Deliver(ctx context.Context, d SharedDelivery) error {
	_, err := a.Submit(ctx, d)
	return err
}
