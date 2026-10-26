package network

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
)

const mailboxWorkers = 128
const headHintRoot = "auriga.internal.network.head."

type mailboxPublisher interface{ Publish(string, []byte) error }

type mailboxRouter struct {
	redeemer        *IAMRedeemer
	client          *MailboxClient
	signer          *errorSigner
	nc              *nats.Conn
	slots           chan struct{}
	ctx             context.Context
	workers         sync.WaitGroup
	subscriptions   []*nats.Subscription
	mu              sync.Mutex
	closing         bool
	publisher       mailboxPublisher
	headPending     map[nodeIdentity]string
	headRunning     map[nodeIdentity]bool
	headSlots       chan struct{}
	upSubscriptions map[string]*nats.Subscription
	ingressNodes    map[string]nodeIdentity
	nodeRefs        map[nodeIdentity]int
	tenantRefs      map[string]int
	nodeBusy        map[nodeIdentity]bool
	nodeBudgets     map[nodeIdentity]*nodeBudget
	tenantBudgets   map[string]*requestBucket
	tenantActive    map[string]int
	errorSlots      chan struct{}
	tenantErrors    map[string]int
	tenantHeads     map[string]int
}

func newMailboxRouter(ctx context.Context, r *IAMRedeemer, c *MailboxClient, s *errorSigner, nc *nats.Conn) (*mailboxRouter, error) {
	router := &mailboxRouter{
		redeemer:        r,
		client:          c,
		signer:          s,
		nc:              nc,
		ctx:             ctx,
		slots:           make(chan struct{}, mailboxWorkers),
		publisher:       nc,
		headPending:     make(map[nodeIdentity]string),
		headRunning:     make(map[nodeIdentity]bool),
		headSlots:       make(chan struct{}, 16),
		upSubscriptions: make(map[string]*nats.Subscription),
		ingressNodes:    make(map[string]nodeIdentity),
		nodeRefs:        make(map[nodeIdentity]int),
		tenantRefs:      make(map[string]int),
		nodeBusy:        make(map[nodeIdentity]bool),
		nodeBudgets:     make(map[nodeIdentity]*nodeBudget),
		tenantBudgets:   make(map[string]*requestBucket),
		tenantActive:    make(map[string]int),
		errorSlots:      make(chan struct{}, 16),
		tenantErrors:    make(map[string]int),
		tenantHeads:     make(map[string]int),
	}
	for _, subject := range []string{headHintRoot + "*"} {
		sub, err := nc.Subscribe(subject, router.receive)
		if err != nil {
			router.close()
			return nil, err
		}
		router.subscriptions = append(router.subscriptions, sub)
		// Broker/client buffering is bounded too; excess Core requests are dropped,
		// and clients retry the same idempotency key. No unbounded goroutine queue.
		if err = sub.SetPendingLimits(256, 4*EncodingMaxPayloadBytes); err != nil {
			router.close()
			return nil, err
		}
	}
	return router, nil
}

func (r *mailboxRouter) close() {
	r.mu.Lock()
	r.closing = true
	for _, sub := range r.subscriptions {
		_ = sub.Unsubscribe()
	}
	for _, sub := range r.upSubscriptions {
		_ = sub.Unsubscribe()
	}
	r.mu.Unlock()
	r.nc.Close()
	r.workers.Wait()
}

func (r *mailboxRouter) receive(msg *nats.Msg) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closing || r.ctx.Err() != nil {
		return
	}
	ingress, err := IngressFromUp(msg.Subject)
	head := strings.HasPrefix(msg.Subject, headHintRoot)
	if head {
		ingress = strings.TrimPrefix(msg.Subject, headHintRoot)
		if !ValidIngressID(ingress) {
			return
		}
	} else if err != nil {
		return
	}
	r.redeemer.mu.Lock()
	b, known := r.redeemer.bindings[ingress]
	if !known {
		r.redeemer.mu.Unlock()
		return
	}
	if head {
		r.redeemer.mu.Unlock()
		if b.Epoch != 0 && !b.Revoked && time.Now().Before(b.NotAfter) {
			r.headPending[nodeIdentity{b.Tenant, b.Realm, b.Node}] = ingress
			r.dispatchHeadsLocked()
		}
		return
	}
	r.redeemer.mu.Unlock()
	node := nodeIdentity{b.Tenant, b.Realm, b.Node}
	if !r.allowRequestLocked(node, time.Now()) {
		return
	}
	var ae *AppError
	switch {
	case b.Revoked:
		ae = &AppError{Code: "network.revoked", Scope: "node"}
	case !time.Now().Before(b.NotAfter):
		ae = &AppError{Code: "network.expired", Scope: "connection"}
	case r.nodeBusy[node] || r.tenantActive[b.Tenant] >= 16:
		ae = &AppError{Code: "network.overloaded", Scope: "request", Retryable: true}
	default:
		select {
		case r.slots <- struct{}{}:
			r.nodeBusy[node] = true
			r.tenantActive[b.Tenant]++
			r.redeemer.mu.Lock()
			r.redeemer.inFlight[ingress] = true
			r.redeemer.mu.Unlock()
		default:
			ae = &AppError{Code: "network.overloaded", Scope: "request", Retryable: true}
		}
	}
	if ae != nil {
		r.scheduleErrorLocked(b, msg.Data, ae)
		return
	}
	r.workers.Go(func() {
		defer func() {
			r.mu.Lock()
			defer r.mu.Unlock()
			r.redeemer.mu.Lock()
			delete(r.redeemer.inFlight, ingress)
			r.redeemer.mu.Unlock()
			<-r.slots
			delete(r.nodeBusy, node)
			r.tenantActive[b.Tenant]--
			if r.tenantActive[b.Tenant] == 0 {
				delete(r.tenantActive, b.Tenant)
			}
			r.dispatchHeadsLocked()
		}()
		r.handle(b, msg.Data, head)
	})
}

func (r *mailboxRouter) dispatchHeadsLocked() {
	if r.closing || r.ctx.Err() != nil {
		return
	}
	for node, ingress := range r.headPending {
		if r.headRunning[node] || r.tenantHeads[node.Tenant] >= 2 {
			continue
		}
		r.redeemer.mu.Lock()
		b, ok := r.redeemer.bindings[ingress]
		busy := r.nodeBusy[node]
		r.redeemer.mu.Unlock()
		if !ok || b.Revoked || b.Epoch == 0 || !time.Now().Before(b.NotAfter) {
			delete(r.headPending, node)
			continue
		}
		if busy {
			continue
		}
		select {
		case r.headSlots <- struct{}{}:
		default:
			return
		}
		delete(r.headPending, node)
		r.headRunning[node] = true
		r.tenantHeads[node.Tenant]++
		r.workers.Go(func() {
			defer func() {
				r.mu.Lock()
				defer r.mu.Unlock()
				delete(r.headRunning, node)
				r.tenantHeads[node.Tenant]--
				if r.tenantHeads[node.Tenant] == 0 {
					delete(r.tenantHeads, node.Tenant)
				}
				<-r.headSlots
				r.dispatchHeadsLocked()
			}()
			r.handle(b, nil, true)
		})
	}
}

func (r *mailboxRouter) handle(b liveBinding, raw []byte, head bool) {
	operation, requestID, ae := mailboxOperation(raw, head)
	if ae != nil {
		if !head {
			r.sendError(b, raw, ae)
		}
		return
	}
	if operation != "ready" && b.Epoch == 0 {
		if !head {
			r.sendError(b, raw, staleEpoch(requestID))
		}
		return
	}
	if operation == "local_register" {
		q, decErr := decodeLocalRegister(raw)
		r.redeemer.mu.Lock()
		current, ok := r.redeemer.bindings[b.Ingress]
		live := ok && sameMailboxBinding(current, b) && !current.Revoked && time.Now().Before(current.NotAfter)
		r.redeemer.mu.Unlock()
		if decErr != nil || !live || registrationString(q["epoch"]) != u64str(b.Epoch) {
			r.sendError(b, raw, staleEpoch(requestID))
			return
		}
	}
	response, ae := r.client.Call(r.ctx, b, operation, raw)
	if ae != nil {
		if !head {
			r.sendError(b, raw, ae)
		}
		return
	}
	if head {
		var tip struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(response, &tip) != nil || tip.Type != "head" {
			return
		}
	}
	updated, err := validateMailboxResponse(b, operation, requestID, raw, response)
	if err != nil {
		if !head {
			r.sendError(b, raw, protocolError(requestID, "invalid collab response"))
		}
		return
	}
	// Re-read after the RPC: a revocation observed while fetching must suppress
	// the completed body, not just the next request. Collab also authorizes each RPC.
	guard := r.redeemer.connectionGuard(b.Ingress)
	if guard == nil {
		return
	}
	guard.Lock()
	r.redeemer.mu.Lock()
	current, ok := r.redeemer.bindings[b.Ingress]
	allowed := ok && !current.Revoked && time.Now().Before(current.NotAfter) && (operation == "ready" || sameMailboxBinding(current, b))
	if allowed {
		if operation == "ready" {
			current.Incarnation, current.ChannelNonce, current.Epoch = updated.Incarnation, updated.ChannelNonce, updated.Epoch
			r.redeemer.bindings[b.Ingress] = current
		}
	} else {
		err = errUnauthorized
	}
	r.redeemer.mu.Unlock()
	// Only this connection's revocation/publication guard spans the socket write;
	// other connections, callout admission and snapshots never wait on network I/O.
	if allowed {
		err = r.publisher.Publish(DownSubject(b.Ingress), response)
	}
	guard.Unlock()
	if errors.Is(err, errUnauthorized) {
		if !head {
			r.sendError(b, raw, &AppError{Code: "network.revoked", Scope: "node"})
		}
	} else if err != nil {
		slog.Error("network mailbox response publish failed", "server", b.ServerID, "cid", b.ClientID)
	}
}

func mailboxOperation(raw []byte, head bool) (string, string, *AppError) {
	if head {
		return "head", "", nil
	}
	if len(raw) == 0 || len(raw) > EncodingMaxPayloadBytes {
		return "", "", protocolError("", "frame size")
	}
	var envelope struct {
		AppVersion   string `json:"app_version"`
		Type         string `json:"type"`
		RequestID    string `json:"request_id"`
		ChannelNonce string `json:"channel_nonce"`
	}
	if json.Unmarshal(raw, &envelope) != nil || envelope.AppVersion != AppVersion || !opaqueID.MatchString(envelope.RequestID) {
		return "", "", protocolError("", "envelope")
	}
	if envelope.Type == "ready" {
		if !opaqueID.MatchString(envelope.ChannelNonce) {
			return "", envelope.RequestID, protocolError(envelope.RequestID, "channel nonce")
		}
	} else if envelope.Type == "local_register" {
		if _, ae := decodeLocalRegister(raw); ae != nil {
			return "", envelope.RequestID, ae
		}
	} else if _, ae := DecodeUpstream(raw); ae != nil {
		return "", envelope.RequestID, ae
	}
	return envelope.Type, envelope.RequestID, nil
}

type mailboxAuth struct {
	Alg          string  `json:"alg"`
	Purpose      string  `json:"purpose"`
	KeyID        string  `json:"kid"`
	Signature    string  `json:"signature"`
	RequestID    *string `json:"request_id"`
	ChannelNonce string  `json:"channel_nonce"`
	Audience     struct {
		Realm       string `json:"realm"`
		Node        string `json:"node"`
		Incarnation string `json:"incarnation"`
		Epoch       string `json:"epoch"`
	} `json:"audience"`
}

type mailboxResponse struct {
	AppVersion   string          `json:"app_version"`
	Type         string          `json:"type"`
	RequestID    string          `json:"request_id"`
	Epoch        string          `json:"epoch"`
	Auth         mailboxAuth     `json:"server_auth"`
	Page         json.RawMessage `json:"page"`
	NodeIdentity struct {
		Realm       string `json:"realm"`
		Node        string `json:"node"`
		Incarnation string `json:"incarnation"`
	} `json:"node_identity"`
}

func validateMailboxResponse(b liveBinding, operation, requestID string, request, response []byte) (liveBinding, error) {
	if operation == "local_register" && validateRegisterResponse(request, response) != nil {
		return b, errUnauthorized
	}
	var frame mailboxResponse
	if json.Unmarshal(response, &frame) != nil || frame.AppVersion != AppVersion {
		return b, errUnauthorized
	}
	expected := map[string]string{"ready": "ready_result", "head": "head", "fetch": "fetch_result", "receipt": "receipt_committed", "local_register": "register_result"}[operation]
	if frame.Type != expected && frame.Type != "error" {
		return b, errUnauthorized
	}
	if frame.Type == "fetch_result" {
		page := frame.Page
		frame = mailboxResponse{}
		if json.Unmarshal(page, &frame) != nil || frame.Type != "fetch_page" || frame.AppVersion != AppVersion {
			return b, errUnauthorized
		}
	}
	auth := frame.Auth
	signature, err := base64.RawURLEncoding.DecodeString(auth.Signature)
	if err != nil || len(signature) != 64 || !keyID.MatchString(auth.KeyID) || auth.KeyID == "stub-unsigned" || auth.Alg != "Ed25519" || auth.Purpose != "protocol_response" || auth.Audience.Realm != b.Realm || auth.Audience.Node != b.Node || !opaqueID.MatchString(auth.Audience.Incarnation) || !opaqueID.MatchString(auth.ChannelNonce) {
		return b, errUnauthorized
	}
	if operation == "head" {
		if auth.RequestID != nil {
			return b, errUnauthorized
		}
	} else if frame.RequestID != requestID || auth.RequestID == nil || *auth.RequestID != requestID {
		return b, errUnauthorized
	}
	if operation == "ready" {
		var req struct {
			Nonce string `json:"channel_nonce"`
		}
		if json.Unmarshal(request, &req) != nil || auth.ChannelNonce != req.Nonce {
			return b, errUnauthorized
		}
		if frame.Type == "ready_result" {
			epoch, err := parseP64("epoch", frame.Epoch)
			if err != nil || auth.Audience.Epoch != frame.Epoch || frame.NodeIdentity.Realm != b.Realm || frame.NodeIdentity.Node != b.Node || frame.NodeIdentity.Incarnation != auth.Audience.Incarnation {
				return b, errUnauthorized
			}
			b.Incarnation, b.ChannelNonce, b.Epoch = auth.Audience.Incarnation, auth.ChannelNonce, epoch
		}
	} else if auth.Audience.Epoch != u64str(b.Epoch) || auth.Audience.Incarnation != b.Incarnation || auth.ChannelNonce != b.ChannelNonce {
		return b, errUnauthorized
	}
	return b, nil
}

func (r *mailboxRouter) sendError(b liveBinding, raw []byte, ae *AppError) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.scheduleErrorLocked(b, raw, ae)
}

func (r *mailboxRouter) writeError(b liveBinding, raw []byte, ae *AppError) {
	if b.Ingress == "" || r.ctx.Err() != nil {
		return
	}
	guard := r.redeemer.connectionGuard(b.Ingress)
	if guard == nil {
		return
	}
	guard.Lock()
	defer guard.Unlock()
	r.redeemer.mu.Lock()
	current, ok := r.redeemer.bindings[b.Ingress]
	r.redeemer.mu.Unlock()
	if !ok || !sameMailboxBinding(current, b) || !time.Now().Before(current.NotAfter) {
		return
	}
	b = current
	if b.Revoked {
		ae = &AppError{Code: "network.revoked", Scope: "node"}
	}
	var request struct {
		RequestID string `json:"request_id"`
		Type      string `json:"type"`
		Nonce     string `json:"channel_nonce"`
	}
	if len(raw) <= EncodingMaxPayloadBytes {
		_ = json.Unmarshal(raw, &request)
	}
	if b.Epoch == 0 && request.Type == "ready" && opaqueID.MatchString(request.Nonce) {
		b.ChannelNonce = request.Nonce
	}
	frame, err := r.signer.encode(b, request.RequestID, ae)
	if err == nil {
		err = r.publisher.Publish(DownSubject(b.Ingress), frame)
	}
	if err != nil {
		slog.Error("network mailbox error response unavailable", "server", b.ServerID, "cid", b.ClientID)
	}
}

func sameMailboxBinding(a, b liveBinding) bool {
	return a.Binding == b.Binding && a.Tenant == b.Tenant && a.Realm == b.Realm && a.Node == b.Node && a.Epoch == b.Epoch && a.Incarnation == b.Incarnation && a.ChannelNonce == b.ChannelNonce
}
