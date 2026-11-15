package observer

import (
	"context"
	"errors"
	"fmt"
	"time"

	nodev1 "github.com/hansjone/auriga/infra/gen/auriga/node/v1"
)

// These typed results distinguish a running resource invalidation from a
// rejected candidate/snapshot. Adapters must not label transport errors revoked.
var ErrPoolResourceExpired = errors.New("pool resource expired")
var ErrPoolResourceRevoked = errors.New("pool resource revoked")
var ErrPoolSnapshotRejected = errors.New("pool snapshot rejected")

// PoolWatch is called by the node observer, never by the hosted plugin. The
// caller supplies fresh authoritative views and delivers the returned reports on
// the authenticated NodeControl stream. Failed delivery must be retried there.
type PoolWatch struct {
	Pools interface {
		Trip(context.Context, string, string, string) (*nodev1.SharedDshGuardianImpact, error)
	}
	Verifier interface {
		Verify(context.Context, string, []*nodev1.SharedDshResourceView) error
	}
	Validate func([]*nodev1.SharedDshBinding, []*nodev1.SharedDshResourceView, int64) error
}

func (w *PoolWatch) Check(ctx context.Context, operation string, bindings []*nodev1.SharedDshBinding, views []*nodev1.SharedDshResourceView, now time.Time) ([]*nodev1.SharedDshResourceReport, *nodev1.SharedDshGuardianImpact, error) {
	if len(bindings) == 0 || w.Pools == nil || w.Validate == nil {
		return nil, nil, fmt.Errorf("missing pool watch source")
	}
	pool := bindings[0].GetPhysicalInstanceId()
	reason := "verified"
	e := w.Validate(bindings, views, now.Unix())
	if e != nil {
		reason = "authorization-mismatch"
	} else if w.Verifier == nil {
		e = fmt.Errorf("verifier unavailable")
		reason = "cleanup-unconfirmed"
	} else if e = w.Verifier.Verify(ctx, pool, views); e != nil {
		reason = "cleanup-unconfirmed"
	}
	trip := ""
	if errors.Is(e, ErrPoolResourceExpired) {
		reason = "expired"
		trip = "resource-expired"
	}
	if errors.Is(e, ErrPoolResourceRevoked) {
		reason = "revoked"
		trip = "resource-revoked"
	}
	var impact *nodev1.SharedDshGuardianImpact
	if trip != "" {
		var err error
		impact, err = w.Pools.Trip(ctx, pool, operation, trip)
		if err != nil {
			return nil, nil, err
		}
	}
	reports := make([]*nodev1.SharedDshResourceReport, 0, len(views))
	for _, v := range views {
		if v == nil {
			continue
		}
		status := "ready"
		if e != nil {
			status = "rejected"
			if impact != nil && impact.State == "stopped" {
				status = "stopped"
			} else if impact != nil {
				reason = "cleanup-unconfirmed"
			}
		}
		reports = append(reports, &nodev1.SharedDshResourceReport{SchemaVersion: 1, Binding: v.Binding, ViewId: v.ViewId, Status: status, Reason: reason, ObservedAt: now.Unix()})
	}
	return reports, impact, nil
}
