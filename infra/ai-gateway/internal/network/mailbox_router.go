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

type mailboxRouter struct {
	redeemer      *IAMRedeemer
	client        *MailboxClient
	signer        *errorSigner
	nc            *nats.Conn
	slots         chan struct{}
	ctx           context.Context
	workers       sync.WaitGroup
	subscriptions []*nats.Subscription
	mu            sync.Mutex
	closing       bool
}

func newMailboxRouter(ctx context.Context, r *IAMRedeemer, c *MailboxClient, s *errorSigner, nc *nats.Conn) (*mailboxRouter, error) {
	router := &mailboxRouter{redeemer: r, client: c, signer: s, nc: nc, ctx: ctx, slots: make(chan struct{}, mailboxWorkers)}
	for _, subject := range []string{UpWildcard, headHintRoot + "*"} {
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
	r.mu.Unlock()
	for _, sub := range r.subscriptions {
		_ = sub.Unsubscribe()
	}
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
	busy := r.redeemer.inFlight[ingress]
	if !known {
		r.redeemer.mu.Unlock()
		return
	}
	var ae *AppError
	switch {
	case b.Revoked:
		ae = &AppError{Code: "network.revoked", Scope: "node"}
	case !time.Now().Before(b.NotAfter):
		ae = &AppError{Code: "network.expired", Scope: "connection"}
	case busy:
		ae = &AppError{Code: "network.overloaded", Scope: "request", Retryable: true}
	default:
		select {
		case r.slots <- struct{}{}:
			r.redeemer.inFlight[ingress] = true
		default:
			ae = &AppError{Code: "network.overloaded", Scope: "request", Retryable: true}
		}
	}
	r.redeemer.mu.Unlock()
	if ae != nil {
		r.sendError(b, msg.Data, ae)
		return
	}
	r.workers.Go(func() {
		defer func() { r.redeemer.mu.Lock(); delete(r.redeemer.inFlight, ingress); r.redeemer.mu.Unlock(); <-r.slots }()
		r.handle(b, msg.Data, head)
	})
}

func (r *mailboxRouter) handle(b liveBinding, raw []byte, head bool) {
	operation, requestID, ae := mailboxOperation(raw, head)
	if ae != nil {
		r.sendError(b, raw, ae)
		return
	}
	if operation != "ready" && b.Epoch == 0 {
		r.sendError(b, raw, staleEpoch(requestID))
		return
	}
	response, ae := r.client.Call(r.ctx, b, operation, raw)
	if ae != nil {
		r.sendError(b, raw, ae)
		return
	}
	updated, err := validateMailboxResponse(b, operation, requestID, raw, response)
	if err != nil {
		r.sendError(b, raw, protocolError(requestID, "invalid collab response"))
		return
	}
	// Re-read after the RPC: a revocation observed while fetching must suppress
	// the completed body, not just the next request. Collab also authorizes each RPC.
	r.redeemer.mu.Lock()
	current, ok := r.redeemer.bindings[b.Ingress]
	if ok && !current.Revoked && time.Now().Before(current.NotAfter) {
		current.Incarnation, current.ChannelNonce, current.Epoch = updated.Incarnation, updated.ChannelNonce, updated.Epoch
		r.redeemer.bindings[b.Ingress] = current
		// Publish while holding the latch lock: reconciliation cannot revoke between
		// this final check and queueing the response on the authenticated data link.
		err = r.nc.Publish(DownSubject(b.Ingress), response)
	} else {
		err = errUnauthorized
	}
	r.redeemer.mu.Unlock()
	if errors.Is(err, errUnauthorized) {
		r.sendError(current, raw, &AppError{Code: "network.revoked", Scope: "node"})
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
	var frame mailboxResponse
	if json.Unmarshal(response, &frame) != nil || frame.AppVersion != AppVersion {
		return b, errUnauthorized
	}
	expected := map[string]string{"ready": "ready_result", "head": "head", "fetch": "fetch_result", "receipt": "receipt_committed"}[operation]
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
	if b.Ingress == "" || r.ctx.Err() != nil {
		return
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
		err = r.nc.Publish(DownSubject(b.Ingress), frame)
	}
	if err != nil {
		slog.Error("network mailbox error response unavailable", "server", b.ServerID, "cid", b.ClientID)
	}
}
