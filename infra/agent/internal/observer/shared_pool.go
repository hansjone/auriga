package observer

import (
	"context"
	"fmt"
	"time"

	nodev1 "github.com/hansjone/auriga/infra/gen/auriga/node/v1"
)

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
	trip := "resource-revoked"
	e := w.Validate(bindings, views, now.Unix())
	if e == nil {
		if w.Verifier == nil {
			e = fmt.Errorf("verifier unavailable")
		} else {
			e = w.Verifier.Verify(ctx, pool, views)
		}
	}
	if e != nil {
		reason = "revoked"
		for _, v := range views {
			for _, r := range v.GetResources() {
				if r.GetExpiresAt() <= now.Unix() {
					reason = "expired"
					trip = "resource-expired"
				}
			}
		}
	}
	var impact *nodev1.SharedDshGuardianImpact
	if e != nil {
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
			if impact.State == "stopped" {
				status = "stopped"
			} else {
				reason = "cleanup-unconfirmed"
			}
		}
		reports = append(reports, &nodev1.SharedDshResourceReport{SchemaVersion: 1, Binding: v.Binding, ViewId: v.ViewId, Status: status, Reason: reason, ObservedAt: now.Unix()})
	}
	return reports, impact, nil
}
