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

func (m *UnifiedManager) checkStoppedRecovery(id string, generation int64) error {
	defer m.lock(id)()
	s, _, _, err := m.load(id)
	if err != nil {
		return err
	}
	if s.needsObservation() && (s.Generation != generation || s.observedGeneration() != uint64(generation)) {
		return fmt.Errorf("resource renewal cannot start a different stopped generation")
	}
	return nil
}

// Startup normally requires the ledger and execution to agree. Renewal is
// allowed to extend the same live view without projecting a new one. This is
// only valid while recovering that very process and the desired spec still
// matches the exact signed intent that Guard validated and stored.
func (m *UnifiedManager) checkStartupView(scope *activationScope, s *unifiedNodeState, reusedProcess bool) error {
	spec := scope.spec
	if s.Generation == spec.Generation && s.observedGeneration() == uint64(spec.Generation) &&
		(scope.recovering || s.SelectionRevision == spec.GetResourceSelectionRevision()) {
		return nil
	}
	if !scope.recovering || !reusedProcess || s.State != "active" || s.ViewGeneration != spec.Generation || s.Generation <= spec.Generation || m.Specs == nil {
		return fmt.Errorf("resource view does not match startup activation")
	}
	latest, err := m.Specs.GetDesired(spec.InstanceId)
	if err != nil {
		return err
	}
	list := unifiedTenantRevocations(latest)
	if latest == nil || latest.GetInstanceId() != s.Host || latest.GetTenantId() != s.Tenant || latest.GetOwnerId() != s.Owner ||
		latest.GetDesired() != nodev1.DesiredState_DESIRED_STATE_RUNNING || latest.GetGeneration() != s.Generation ||
		latest.GetResourceSelectionRevision() != s.SelectionRevision || len(latest.GetUnifiedResources()) != len(s.Installations) ||
		(latest.AllowPersonalPlugins == nil || latest.GetAllowPersonalPlugins()) != s.AllowPersonalPlugins ||
		list.GetRevision() != s.RevocationRevision {
		return fmt.Errorf("desired state does not match the authorized resource renewal")
	}
	document, err := unifiedProto(list)
	if err != nil {
		return err
	}
	delete(document, "signature")
	canonical, err := unifiedCanonical(document)
	if err != nil || udigest(canonical) != s.RevocationDigest {
		return fmt.Errorf("revocations do not match the authorized resource renewal")
	}
	expected := make(map[string]*resourcev1.UnifiedResourceRef, len(s.Installations))
	for _, record := range s.Installations {
		ref := new(resourcev1.UnifiedResourceRef)
		if err := proto.Unmarshal(record.Ref, ref); err != nil {
			return err
		}
		expected[ref.GetIdentity().GetPackageId()] = ref
	}
	for _, ref := range latest.GetUnifiedResources() {
		pkg := ref.GetIdentity().GetPackageId()
		prior := expected[pkg]
		if prior == nil || !proto.Equal(ref, prior) {
			return fmt.Errorf("desired state changed the authorized resource renewal")
		}
		delete(expected, pkg)
	}
	if len(expected) != 0 {
		return fmt.Errorf("desired state omitted an authorized resource")
	}
	return nil
}

// Publication after a definite intent commit is read-only. Recheck the view
// and lease we verified; cleanup requiring Engine writes stays inside Observe.
// The returned check uses the verified lease and live clocks without loading
// the ledger again. Public observation may already hold the resource lock.
func (m *UnifiedManager) recheckStartupView(scope *activationScope, revision uint64, reusedProcess bool) (func() error, error) {
	id := scope.spec.InstanceId
	defer m.lock(id)()
	s, _, _, err := m.load(id)
	if err != nil {
		return nil, err
	}
	if s.Revision != revision {
		return nil, fmt.Errorf("resource ledger changed before endpoint publication")
	}
	if err := m.checkStartupView(scope, s, reusedProcess); err != nil {
		return nil, err
	}
	if s.State != "active" {
		return nil, nil
	}
	clock, expiresAt := s.Clock, s.LeaseExpiresAt
	check := func() error { return m.checkLeaseClock(clock, expiresAt) }
	return check, check()
}

func (m *UnifiedManager) checkLeaseClock(clock map[string]any, expiresAt string) error {
	return m.Protocol.Evaluate("clock", map[string]any{
		"clock": clock, "bootId": m.BootID,
		"monotonicNowMillis": json.Number(fmt.Sprint(m.mono())),
		"wallNow":            m.now().Format(time.RFC3339Nano), "leaseExpiresAt": expiresAt,
	})
}

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
	if s.ViewGeneration == 0 {
		s.ViewGeneration = s.Generation
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
		if id.GetTenantId() != s.Tenant || !uUUID.MatchString(id.GetPackageId()) || !uUUID.MatchString(id.GetVersionId()) || (id.GetKind() != "skill" && id.GetKind() != "plugin") {
			return fmt.Errorf("invalid installation receipt identity")
		}
		identity, err := unifiedProto(id)
		if err != nil {
			return err
		}
		receipt := map[string]any{"schemaVersion": 1, "tenantId": s.Tenant, "ownerId": s.Owner, "hostId": s.Host, "identity": identity, "installationRevision": record.InstallationRevision, "selectionRevision": s.SelectionRevision, "generation": s.observedGeneration(), "state": record.State, "desiredState": record.DesiredState, "recordedAt": m.now().Format(time.RFC3339Nano)}
		file := filepath.Join(root, "installations", s.Tenant, s.Owner, s.Host, id.GetKind(), id.GetPackageId(), id.GetVersionId(), fmt.Sprint(record.InstallationRevision)+".json")
		if err := unifiedAtomic(file, receipt); err != nil {
			return err
		}
	}
	return nil
}
