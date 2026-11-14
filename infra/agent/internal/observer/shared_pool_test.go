package observer_test

import (
	"context"
	"errors"
	"github.com/hansjone/auriga/infra/agent/internal/observer"
	nodev1 "github.com/hansjone/auriga/infra/gen/auriga/node/v1"
	"testing"
	"time"
)

type watchTrip struct{ state, reason string }

func (w *watchTrip) Trip(_ context.Context, pool, op, reason string) (*nodev1.SharedDshGuardianImpact, error) {
	w.reason = reason
	return &nodev1.SharedDshGuardianImpact{Scope: "pool", State: w.state}, nil
}
func TestPoolWatchExpiryAndMissingVerifier(t *testing.T) {
	tr := &watchTrip{state: "stopped"}
	w := &observer.PoolWatch{Pools: tr, Validate: func([]*nodev1.SharedDshBinding, []*nodev1.SharedDshResourceView, int64) error {
		return observer.ErrPoolResourceExpired
	}}
	views := []*nodev1.SharedDshResourceView{{Binding: &nodev1.SharedDshBinding{MemberId: "a"}, Resources: []*nodev1.SharedDshResource{{ExpiresAt: 10}}}, {Binding: &nodev1.SharedDshBinding{MemberId: "b"}}}
	bindings := []*nodev1.SharedDshBinding{views[0].Binding, views[1].Binding}
	reports, impact, e := w.Check(context.Background(), "op", bindings, views, time.Unix(10, 0))
	if e != nil || impact.State != "stopped" || tr.reason != "resource-expired" || len(reports) != 2 {
		t.Fatal(reports, impact, e)
	}
	for _, r := range reports {
		if r.Status != "stopped" || r.Reason != "expired" {
			t.Fatal(r)
		}
	}
	w.Validate = func([]*nodev1.SharedDshBinding, []*nodev1.SharedDshResourceView, int64) error { return nil }
	tr.state = "blocked"
	reports, impact, e = w.Check(context.Background(), "op", bindings, views, time.Unix(1, 0))
	if e != nil || impact != nil {
		t.Fatal(impact, e)
	}
	for _, r := range reports {
		if r.Status != "rejected" || r.Reason != "cleanup-unconfirmed" {
			t.Fatal(r)
		}
	}
}

func TestPoolWatchMismatchDoesNotStopRunningPool(t *testing.T) {
	for _, failure := range []error{observer.ErrPoolSnapshotRejected, errors.New("incomplete snapshot"), errors.New("authorization-mismatch")} {
		tr := &watchTrip{}
		w := &observer.PoolWatch{Pools: tr, Validate: func([]*nodev1.SharedDshBinding, []*nodev1.SharedDshResourceView, int64) error { return failure }}
		views := []*nodev1.SharedDshResourceView{{Binding: &nodev1.SharedDshBinding{MemberId: "candidate"}}}
		reports, impact, err := w.Check(context.Background(), "op", []*nodev1.SharedDshBinding{views[0].Binding}, views, time.Unix(10, 0))
		if err != nil || impact != nil || tr.reason != "" || len(reports) != 1 || reports[0].Status != "rejected" || reports[0].Reason != "authorization-mismatch" {
			t.Fatal(reports, impact, err)
		}
	}
}
