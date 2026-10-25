package modelgate

import (
	"context"
	"time"

	"github.com/hansjone/auriga/infra/ai-gateway/internal/admin"
	api "github.com/hansjone/auriga/infra/gen/aiapi"
)

type Gate struct {
	authority *Authority
	reader    *admin.RelayReader
}

func New(authority *Authority, reader *admin.RelayReader) (*Gate, error) {
	if authority == nil || reader == nil || authority.currency != reader.Currency() {
		return nil, fault("common.validation_failed")
	}
	return &Gate{authority: authority, reader: reader}, nil
}

// Call stores preparation constraints only. It deliberately has no "safe",
// "allowed" or "ready" flag: policy/metering and budget are outside this task.
type Call struct {
	gate     *Gate
	raw      string
	identity *Identity
	snapshot admin.RelaySnapshot
}

func (c Call) Identity() Identity            { return *c.identity }
func (c Call) Snapshot() admin.RelaySnapshot { return c.snapshot }
func (c Call) String() string                { return "AI relay preparation" }
func (c Call) GoString() string              { return c.String() }

func (g *Gate) Catalog(ctx context.Context, raw string) (api.ModelList, error) {
	id, e := g.authority.Check(ctx, raw)
	if e != nil {
		return api.ModelList{}, e
	}
	page, e := g.reader.Catalog(ctx, id.tenant, id.account)
	if e != nil {
		return api.ModelList{}, e
	}
	if !id.expires.After(g.authority.now()) {
		return api.ModelList{}, fault("common.unauthenticated")
	}
	return page, nil
}
func (g *Gate) Prepare(ctx context.Context, raw, model string) (*Call, error) {
	id, e := g.authority.Check(ctx, raw)
	if e != nil {
		return nil, e
	}
	if e = id.InvocationQuota(); e != nil {
		return nil, e
	}
	snap, e := g.reader.Capture(ctx, id.tenant, id.account, model)
	if e != nil {
		return nil, e
	}
	if !id.expires.After(g.authority.now()) {
		return nil, fault("common.unauthenticated")
	}
	return &Call{gate: g, raw: raw, identity: id, snapshot: snap}, nil
}

// Recheck performs all online checks before taking any DB lock/connection. New
// admissions reject changed quota or revisions; existing streams retain their
// pinned quota and key under D3. Neither mode extends the original deadline.
// Callers must cancel streams on failures and enforce ExpiresAt independently;
// events can accelerate this check but cannot replace it with an allow cache.
func (g *Gate) Recheck(ctx context.Context, c *Call, inFlight bool) error {
	if c == nil || c.gate != g {
		return fault("common.validation_failed")
	}
	if !c.identity.expires.After(g.authority.now()) {
		return fault("common.unauthenticated")
	}
	id, e := g.authority.Check(ctx, c.raw)
	if e != nil {
		return e
	}
	if id.tenant != c.identity.tenant || id.account != c.identity.account || id.device != c.identity.device || id.expires.Before(c.identity.expires) {
		return fault("common.unauthenticated")
	}
	if !inFlight {
		if e = id.InvocationQuota(); e != nil {
			return e
		}
		if id.limits != c.identity.limits {
			return fault("ai.budget_unavailable")
		}
	}
	if e = g.reader.Recheck(ctx, c.snapshot, inFlight); e != nil {
		return e
	}
	if !c.identity.expires.After(g.authority.now()) {
		return fault("common.unauthenticated")
	}
	return nil
}

// Deadline is the immutable original lease, for context.WithDeadline at the
// handler. A later IAM response never extends the lifetime of an existing call.
func (c *Call) Deadline() time.Time { return c.identity.expires }
