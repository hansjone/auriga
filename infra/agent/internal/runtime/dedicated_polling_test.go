package runtime_test

import (
	"context"
	"strings"
	"testing"

	nodev1 "github.com/hansjone/auriga/infra/gen/auriga/node/v1"
	"google.golang.org/protobuf/proto"
)

type dedicatedPollingVerifier struct {
	view                *nodev1.SharedDshResourceView
	refreshes, verified int
}

func (v *dedicatedPollingVerifier) Refresh(context.Context, *nodev1.SharedDshBinding) (*nodev1.SharedDshResourceView, error) {
	v.refreshes++
	return proto.Clone(v.view).(*nodev1.SharedDshResourceView), nil
}
func (v *dedicatedPollingVerifier) Verify(context.Context, string, []*nodev1.SharedDshResourceView) error {
	v.verified++
	return nil
}
func (*dedicatedPollingVerifier) Cleanup(context.Context, string) error { return nil }
func (*dedicatedPollingVerifier) DedicatedIntent(string, *nodev1.SharedDshResourceView) (string, string, error) {
	return "synthetic-readonly-cache", "sha256:" + strings.Repeat("a", 64), nil
}

func TestPersistentDedicatedPollsUnchangedPlacementAndRequiresActualHost(t *testing.T) {
	m, _, _ := persistentFixture(t)
	command := persistentIntent(1)
	command.Binding.Mode = nodev1.DshInstanceMode_DSH_INSTANCE_MODE_DEDICATED
	if receipt, err := m.Pools.Apply(t.Context(), command); err != nil || receipt.Outcome != "confirmed" {
		t.Fatal(err)
	}
	v := &dedicatedPollingVerifier{view: persistentView(command)}
	m.Resources = v
	reports := []*nodev1.SharedDshResourceReport{}
	m.Report = func(frame *nodev1.SessionRequest) {
		if report := frame.GetSharedDshResourceReport(); report != nil {
			reports = append(reports, report)
		}
	}
	for _, id := range []string{"00000000-0000-4000-8000-000000000051", "00000000-0000-4000-8000-000000000052"} {
		v.view.ViewId = id
		m.Watch(t.Context())
		if len(reports) == 0 || reports[len(reports)-1].ViewId != id || reports[len(reports)-1].Status != "rejected" || reports[len(reports)-1].Reason != "cleanup-unconfirmed" {
			t.Fatal("bytes-only verification became ready or skipped a new selection", reports)
		}
	}
	if v.refreshes != 2 || v.verified != 2 {
		t.Fatal("stable binding/digest bypassed placement reconciliation", v.refreshes, v.verified)
	}
}
