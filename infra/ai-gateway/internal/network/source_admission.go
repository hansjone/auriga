package network

import (
	"context"
	"encoding/json"
	"time"

	"github.com/hansjone/auriga/infra/ai-gateway/internal/observability"
)

type sourceLane struct {
	node   nodeIdentity
	revoke bool
}

type sourceCall struct {
	ingress string
	cancel  context.CancelFunc
}

// Called with r.mu held. There is no waiting queue: Register/Get share one
// per-node slot; Revoke has its own slot, global pool and tenant/rate budget.
// Ordinary source calls share the existing 128 workers / 16 per tenant.
// The reserved revocation pool adds at most 16 workers / 2 per tenant.
func (r *mailboxRouter) receiveSourceLocked(b liveBinding, raw []byte) bool {
	if len(raw) == 0 || len(raw) > EncodingMaxPayloadBytes {
		return false
	}
	var tip map[string]json.RawMessage
	if json.Unmarshal(raw, &tip) != nil {
		return false
	}
	op := registrationString(tip["type"])
	if !sourceOperation(op) {
		return false
	}
	node := nodeIdentity{b.Tenant, b.Realm, b.Node}
	lane := sourceLane{node, op == "source_revoke"}
	now := time.Now()
	if lane.revoke {
		if r.revokeBudgets[node] == nil {
			r.revokeBudgets[node] = &requestBucket{}
		}
		if r.tenantRevokeBudgets[b.Tenant] == nil {
			r.tenantRevokeBudgets[b.Tenant] = &requestBucket{}
		}
		if !r.revokeBudgets[node].take(now, 64, 8) || !r.tenantRevokeBudgets[b.Tenant].take(now, 256, 32) {
			return true
		}
	} else if !r.allowRequestLocked(node, now) {
		return true
	}
	slots, active, limit := r.slots, r.tenantActive, 16
	if lane.revoke {
		slots, active, limit = r.sourceRevokes, r.tenantRevokes, 2
	}
	var ae *AppError
	switch {
	case b.Revoked:
		ae = &AppError{Code: "network.revoked", Scope: "node"}
	case !now.Before(b.NotAfter):
		ae = &AppError{Code: "network.expired", Scope: "connection"}
	case r.sources[lane] != nil || active[b.Tenant] >= limit:
		ae = &AppError{Code: "network.overloaded", Scope: "request", Retryable: true}
	default:
		select {
		case slots <- struct{}{}:
		default:
			ae = &AppError{Code: "network.overloaded", Scope: "request", Retryable: true}
		}
	}
	if ae != nil {
		r.scheduleErrorLocked(b, raw, ae)
		return true
	}
	// Starts at admission, before scheduling/validation/RPC. A shorter parent
	// deadline is useful to callers as well; production always caps this at 30s.
	ctx, cancel := context.WithDeadline(r.ctx, now.Add(sourceRequestTimeout))
	call := &sourceCall{b.Ingress, cancel}
	r.sources[lane] = call
	active[b.Tenant]++
	r.workers.Go(func() {
		defer func() {
			cancel()
			r.mu.Lock()
			defer r.mu.Unlock()
			if r.sources[lane] == call {
				delete(r.sources, lane)
			}
			<-slots
			active[b.Tenant]--
			if active[b.Tenant] == 0 {
				delete(active, b.Tenant)
			}
			r.dispatchHeadsLocked()
		}()
		r.handleContext(observability.NATSContext(ctx, raw), b, raw, false)
	})
	return true
}
