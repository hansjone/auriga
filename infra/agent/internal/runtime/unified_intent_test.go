package runtime

import (
	"context"
	"testing"

	resourcev1 "github.com/hansjone/auriga/infra/gen/auriga/resource/v1"
	"google.golang.org/protobuf/proto"
)

func TestUnifiedGeneratedIntentFailsClosed(t *testing.T) {
	f := newUnifiedFixture(t)
	for _, name := range []string{"missing-revision", "unsafe-revision", "missing-state", "unspecified", "unknown", "contradictory"} {
		t.Run(name, func(t *testing.T) {
			ref := proto.Clone(f.ref).(*resourcev1.UnifiedResourceRef)
			switch name {
			case "missing-revision":
				ref.InstallationRevision = 0
			case "unsafe-revision":
				ref.InstallationRevision = 9007199254740992
			case "missing-state":
				ref.DesiredState = nil
			case "unspecified":
				ref.DesiredState = resourcev1.UnifiedResourceDesiredState_UNIFIED_RESOURCE_DESIRED_STATE_UNSPECIFIED.Enum()
			case "unknown":
				ref.DesiredState = resourcev1.UnifiedResourceDesiredState(99).Enum()
			case "contradictory":
				ref.DesiredState = resourcev1.UnifiedResourceDesiredState_UNIFIED_RESOURCE_DESIRED_STATE_REMOVED.Enum()
			}
			if _, _, err := ReadUnifiedIntent(ref); err == nil {
				t.Fatal("invalid generated intent admitted")
			}
		})
	}
}

func TestUnifiedGeneratedDesiredSuccessReports(t *testing.T) {
	for _, test := range []struct {
		desired resourcev1.UnifiedResourceDesiredState
		report  string
	}{
		{resourcev1.UnifiedResourceDesiredState_UNIFIED_RESOURCE_DESIRED_STATE_ENABLED, "applied"},
		{resourcev1.UnifiedResourceDesiredState_UNIFIED_RESOURCE_DESIRED_STATE_DISABLED, "disabled"},
		{resourcev1.UnifiedResourceDesiredState_UNIFIED_RESOURCE_DESIRED_STATE_REMOVED, "removed"},
	} {
		t.Run(test.report, func(t *testing.T) {
			f := newUnifiedFixture(t)
			f.ref.DesiredState = test.desired.Enum()
			f.ref.Enabled = test.report == "applied"
			f.catalogPrivate = !f.ref.Enabled
			if _, err := f.manager.Prepare(context.Background(), f.source.spec); err != nil {
				t.Fatal(err)
			}
			reports, _, err := f.manager.Observe(context.Background(), f.source.spec.InstanceId)
			if err != nil || len(reports) != 1 || reports[0].State != test.report || reports[0].InstallationRevision != 1 {
				t.Fatalf("wrong success mapping: %+v %v", reports, err)
			}
			s, root, _, err := f.manager.load(f.source.spec.InstanceId)
			if err != nil {
				t.Fatal(err)
			}
			s.Installations[0].DesiredState = "invalid"
			if err := f.manager.save(root, s); err != nil {
				t.Fatal(err)
			}
			if _, _, err := f.manager.Observe(context.Background(), f.source.spec.InstanceId); err == nil {
				t.Fatal("ledger intent mismatch reported success")
			}
		})
	}
}
