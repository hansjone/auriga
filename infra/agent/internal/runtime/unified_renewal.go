package runtime

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"time"

	nodev1 "github.com/hansjone/auriga/infra/gen/auriga/node/v1"
	resourcev1 "github.com/hansjone/auriga/infra/gen/auriga/resource/v1"
	"google.golang.org/protobuf/proto"
)

// Only an unchanged selection can renew the running view. Cached original bytes
// are reverified against new signatures and the newest signed revocation list.
func (m *UnifiedManager) renewCached(spec *nodev1.InstanceSpec, s *unifiedNodeState, root string, list *resourcev1.ResourceRevocationList, now time.Time) error {
	if spec.GetResourceSelectionRevision() != s.SelectionRevision || spec.GetGeneration() <= s.Generation {
		return nil
	}
	byPackage := map[string]*resourcev1.UnifiedResourceRef{}
	for _, ref := range spec.GetUnifiedResources() {
		key := ref.GetIdentity().GetPackageId()
		if byPackage[key] != nil {
			return fmt.Errorf("duplicate renewal identity")
		}
		byPackage[key] = ref
	}
	records := []unifiedInstalled{}
	var deadline time.Time
	for _, record := range s.Installations {
		var prior resourcev1.UnifiedResourceRef
		if err := proto.Unmarshal(record.Ref, &prior); err != nil {
			return err
		}
		next := byPackage[prior.GetIdentity().GetPackageId()]
		if next == nil {
			if s.ObservedAt != "" && record.DesiredState == "REMOVED" && record.State == "removed" {
				continue
			}
			return fmt.Errorf("renewal lost identity")
		}
		delete(byPackage, prior.GetIdentity().GetPackageId())
		revision, state, err := ReadUnifiedIntent(next)
		if err != nil || revision != record.InstallationRevision || state != record.DesiredState {
			return fmt.Errorf("renewal changed installation intent")
		}
		a, e1 := unifiedSelectionBytes(&prior)
		b, e2 := unifiedSelectionBytes(next)
		if e1 != nil || e2 != nil || !bytes.Equal(a, b) {
			return fmt.Errorf("renewal changed selected bytes")
		}
		if state == "ENABLED" {
			if _, err := m.validate(root, next, list, now); err != nil {
				return err
			}
			end, err := time.Parse(time.RFC3339Nano, next.GetLease().GetExpiresAt())
			if err != nil || !end.After(now) {
				return fmt.Errorf("renewal already expired")
			}
			if deadline.IsZero() || end.Before(deadline) {
				deadline = end
			}
		}
		wire, err := proto.Marshal(next)
		if err != nil {
			return err
		}
		record.Ref = wire
		records = append(records, record)
	}
	if len(byPackage) != 0 {
		return fmt.Errorf("renewal added identity")
	}
	if deadline.IsZero() {
		return fmt.Errorf("active renewal has no enabled resource")
	}
	if err := m.lockSelection(spec, s); err != nil {
		return err
	}
	s.Installations, s.Generation = records, spec.GetGeneration()
	s.ObservedAt = ""
	s.LeaseExpiresAt = deadline.Format(time.RFC3339Nano)
	s.Clock["leaseDeadlineMonotonicMillis"] = json.Number(fmt.Sprint(m.mono() + deadline.Sub(now).Milliseconds()))
	return nil
}

func (m *UnifiedManager) writeInstallationReceipts(root string, s *unifiedNodeState) error {
	if !uUUID.MatchString(s.Tenant) || !uUUID.MatchString(s.Owner) || !uUUID.MatchString(s.Host) {
		return fmt.Errorf("invalid installation receipt owner")
	}
	for _, record := range s.Installations {
		var ref resourcev1.UnifiedResourceRef
		if err := proto.Unmarshal(record.Ref, &ref); err != nil {
			return err
		}
		id := ref.GetIdentity()
		if id.GetTenantId() != s.Tenant || !uUUID.MatchString(id.GetPackageId()) || !uUUID.MatchString(id.GetVersionId()) || id.GetKind() != "skill" {
			return fmt.Errorf("invalid installation receipt identity")
		}
		identity, err := unifiedProto(id)
		if err != nil {
			return err
		}
		receipt := map[string]any{"schemaVersion": 1, "tenantId": s.Tenant, "ownerId": s.Owner, "hostId": s.Host, "identity": identity, "installationRevision": record.InstallationRevision, "selectionRevision": s.SelectionRevision, "generation": s.Generation, "state": record.State, "desiredState": record.DesiredState, "recordedAt": m.now().Format(time.RFC3339Nano)}
		file := filepath.Join(root, "installations", s.Tenant, s.Owner, s.Host, "skill", id.GetPackageId(), id.GetVersionId(), fmt.Sprint(record.InstallationRevision)+".json")
		if err := unifiedAtomic(file, receipt); err != nil {
			return err
		}
	}
	return nil
}
