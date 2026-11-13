package runtime

import (
	"bytes"
	"errors"
	"fmt"
	"sort"

	nodev1 "github.com/hansjone/auriga/infra/gen/auriga/node/v1"
	resourcev1 "github.com/hansjone/auriga/infra/gen/auriga/resource/v1"
	"google.golang.org/protobuf/proto"
)

var errUnifiedSelection = errors.New("immutable resource selection conflict")

type unifiedSelectionLock struct {
	Revision  uint64
	Resources map[string][]byte
	Collected map[string]bool
}

// Signing times, signatures and download tickets can renew; selected bytes and
// installation intent cannot change under the same tenant/owner revision.
func unifiedSelectionBytes(ref *resourcev1.UnifiedResourceRef) ([]byte, error) {
	if _, _, err := ReadUnifiedIntent(ref); err != nil {
		return nil, err
	}
	copyRef := proto.Clone(ref).(*resourcev1.UnifiedResourceRef)
	copyRef.Lease = nil
	copyRef.ManifestDownloadPath = ""
	if copyRef.Envelope != nil {
		copyRef.Envelope.NotAfter = ""
		copyRef.Envelope.Signature = nil
		if copyRef.Envelope.Statement != nil {
			copyRef.Envelope.Statement.IssuedAt = ""
		}
	}
	for _, artifact := range copyRef.Artifacts {
		artifact.DownloadPath = ""
	}
	sort.Slice(copyRef.Artifacts, func(i, j int) bool { return copyRef.Artifacts[i].ArtifactId < copyRef.Artifacts[j].ArtifactId })
	return (proto.MarshalOptions{Deterministic: true}).Marshal(copyRef)
}

func (m *UnifiedManager) lockSelection(spec *nodev1.InstanceSpec, s *unifiedNodeState) error {
	key := spec.GetTenantId() + "/" + spec.GetOwnerId()
	if s.SelectionLocks == nil {
		s.SelectionLocks = map[string]unifiedSelectionLock{}
	}
	before, exists := s.SelectionLocks[key]
	if !exists && s.Tenant == spec.GetTenantId() && s.Owner == spec.GetOwnerId() && s.SelectionRevision > 0 {
		before = unifiedSelectionLock{Revision: s.SelectionRevision, Resources: map[string][]byte{}, Collected: map[string]bool{}}
		for _, record := range s.Installations {
			var ref resourcev1.UnifiedResourceRef
			if err := proto.Unmarshal(record.Ref, &ref); err != nil {
				return err
			}
			raw, err := unifiedSelectionBytes(&ref)
			if err != nil {
				return err
			}
			before.Resources[ref.GetIdentity().GetPackageId()] = raw
		}
		exists = true
	}
	next := unifiedSelectionLock{Revision: spec.GetResourceSelectionRevision(), Resources: map[string][]byte{}, Collected: map[string]bool{}}
	for _, ref := range spec.GetUnifiedResources() {
		pkg := ref.GetIdentity().GetPackageId()
		if _, duplicate := next.Resources[pkg]; duplicate {
			return errUnifiedSelection
		}
		raw, err := unifiedSelectionBytes(ref)
		if err != nil {
			return err
		}
		next.Resources[pkg] = raw
	}
	if exists {
		if next.Revision < before.Revision {
			return errUnifiedSelection
		}
		if next.Revision == before.Revision {
			for pkg, raw := range next.Resources {
				if before.Collected[pkg] {
					return errUnifiedSelection
				}
				if !bytes.Equal(before.Resources[pkg], raw) {
					var prior, incoming resourcev1.UnifiedResourceRef
					if proto.Unmarshal(before.Resources[pkg], &prior) != nil || proto.Unmarshal(raw, &incoming) != nil ||
						prior.GetDesiredState() != resourcev1.UnifiedResourceDesiredState_UNIFIED_RESOURCE_DESIRED_STATE_ENABLED ||
						incoming.GetDesiredState() != resourcev1.UnifiedResourceDesiredState_UNIFIED_RESOURCE_DESIRED_STATE_DISABLED ||
						incoming.GetInstallationRevision() <= prior.GetInstallationRevision() {
						return errUnifiedSelection
					}
					prior.InstallationRevision = incoming.InstallationRevision
					prior.DesiredState = incoming.DesiredState
					prior.Enabled = incoming.Enabled
					if !proto.Equal(&prior, &incoming) {
						return errUnifiedSelection
					}
					before.Resources[pkg] = raw
				}
			}
			collected := map[string]bool{}
			for pkg, raw := range before.Resources {
				if _, present := next.Resources[pkg]; present {
					continue
				}
				if before.Collected[pkg] {
					collected[pkg] = true
					continue
				}
				var ref resourcev1.UnifiedResourceRef
				if proto.Unmarshal(raw, &ref) != nil || ref.GetDesiredState() != resourcev1.UnifiedResourceDesiredState_UNIFIED_RESOURCE_DESIRED_STATE_REMOVED {
					return errUnifiedSelection
				}
				confirmed := false
				for _, record := range s.Installations {
					var prior resourcev1.UnifiedResourceRef
					if s.ObservedAt != "" && proto.Unmarshal(record.Ref, &prior) == nil && prior.GetIdentity().GetPackageId() == pkg && record.State == "removed" && record.InstallationRevision == ref.GetInstallationRevision() {
						confirmed = true
					}
				}
				if !confirmed {
					return fmt.Errorf("%w: unconfirmed tombstone collection", errUnifiedSelection)
				}
				collected[pkg] = true
			}
			next.Resources = before.Resources
			next.Collected = collected
		}
	}
	s.SelectionLocks[key] = next
	return nil
}
