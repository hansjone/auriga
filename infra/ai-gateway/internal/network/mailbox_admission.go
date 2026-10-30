package network

import (
	"errors"
	"time"
)

// A node can use at most a quarter of its tenant's sustained request budget.
// State exists only for live authenticated identities, not untrusted subjects.
type requestBucket struct {
	updated time.Time
	tokens  float64
}

func (b *requestBucket) take(now time.Time, rate, burst float64) bool {
	if b.updated.IsZero() {
		b.tokens = burst
	} else {
		b.tokens = min(burst, b.tokens+max(0, now.Sub(b.updated).Seconds())*rate)
	}
	b.updated = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

type nodeBudget struct{ requests, errors requestBucket }

func (r *mailboxRouter) budgetLocked(node nodeIdentity) *nodeBudget {
	budget := r.nodeBudgets[node]
	if budget == nil {
		budget = &nodeBudget{}
		r.nodeBudgets[node] = budget
	}
	return budget
}
func (r *mailboxRouter) allowRequestLocked(node nodeIdentity, now time.Time) bool {
	if !r.budgetLocked(node).requests.take(now, 64, 8) {
		return false
	}
	tenant := r.tenantBudgets[node.Tenant]
	if tenant == nil {
		tenant = &requestBucket{}
		r.tenantBudgets[node.Tenant] = tenant
	}
	return tenant.take(now, 256, 32)
}

// Error signing and socket writes never execute on a NATS callback. Flooded
// requests are dropped before decoding, with bounded diagnostic responses.
func (r *mailboxRouter) scheduleErrorLocked(b liveBinding, raw []byte, ae *AppError) {
	r.redeemer.mu.Lock()
	_, known := r.redeemer.bindings[b.Ingress]
	r.redeemer.mu.Unlock()
	if !known {
		return
	}
	if r.closing || r.ctx.Err() != nil || len(raw) > EncodingMaxPayloadBytes || r.tenantErrors[b.Tenant] >= 2 {
		return
	}
	node := nodeIdentity{b.Tenant, b.Realm, b.Node}
	if !r.budgetLocked(node).errors.take(time.Now(), 1, 4) {
		return
	}
	select {
	case r.errorSlots <- struct{}{}:
	default:
		return
	}
	r.tenantErrors[b.Tenant]++
	r.workers.Go(func() {
		defer func() {
			r.mu.Lock()
			defer r.mu.Unlock()
			<-r.errorSlots
			r.tenantErrors[b.Tenant]--
			if r.tenantErrors[b.Tenant] == 0 {
				delete(r.tenantErrors, b.Tenant)
			}
		}()
		r.writeError(b, raw, ae)
	})
}

// Independent exact subscriptions keep one ingress's pending overflow out of
// every other node's queue. Registration occurs before the auth grant is sent.
func (r *mailboxRouter) registerIngress(ingress string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closing || r.ctx.Err() != nil {
		return errors.New("network: mailbox router closed")
	}
	if r.upSubscriptions[ingress] != nil {
		return nil
	}
	r.redeemer.mu.Lock()
	b, ok := r.redeemer.bindings[ingress]
	r.redeemer.mu.Unlock()
	if !ok {
		return errUnauthorized
	}
	sub, err := r.nc.Subscribe(UpSubject(ingress), r.receive)
	if err != nil {
		return err
	}
	if err = sub.SetPendingLimits(8, 2*EncodingMaxPayloadBytes); err != nil {
		_ = sub.Unsubscribe()
		return err
	}
	node := nodeIdentity{b.Tenant, b.Realm, b.Node}
	r.upSubscriptions[ingress] = sub
	r.ingressNodes[ingress] = node
	r.nodeRefs[node]++
	r.tenantRefs[b.Tenant]++
	return nil
}

func (r *mailboxRouter) forgetIngress(ingress string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, call := range r.sources {
		if call.ingress == ingress {
			call.cancel()
		}
	}
	r.redeemer.mu.Lock()
	delete(r.sourceBoot, ingress)
	r.redeemer.mu.Unlock()
	sub := r.upSubscriptions[ingress]
	if sub == nil {
		return
	}
	_ = sub.Unsubscribe()
	node := r.ingressNodes[ingress]
	delete(r.upSubscriptions, ingress)
	delete(r.ingressNodes, ingress)
	r.nodeRefs[node]--
	if r.nodeRefs[node] == 0 {
		delete(r.nodeRefs, node)
		delete(r.nodeBudgets, node)
		delete(r.headPending, node)
		delete(r.revokeBudgets, node)
	}
	r.tenantRefs[node.Tenant]--
	if r.tenantRefs[node.Tenant] == 0 {
		delete(r.tenantRefs, node.Tenant)
		delete(r.tenantBudgets, node.Tenant)
		delete(r.tenantRevokeBudgets, node.Tenant)
	}
}
