package runtime

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/hansjone/auriga/infra/agent/internal/storage"

	nodev1 "github.com/hansjone/auriga/infra/gen/auriga/node/v1"
	resourcev1 "github.com/hansjone/auriga/infra/gen/auriga/resource/v1"
	"google.golang.org/protobuf/proto"
)

// AdmitMigration persists only independently verified A/M evidence. P1 does not
// activate plugin v2; this boundary is usable by migration/recovery without
// weakening skill activation or treating a server mapping as proof.
func (m *UnifiedManager) AdmitMigration(id string, input map[string]any) error {
	defer m.lock(id)()
	if m.Specs == nil {
		return fmt.Errorf("trusted instance source missing")
	}
	spec, err := m.Specs.GetDesired(id)
	if err != nil || spec == nil {
		return fmt.Errorf("trusted instance missing")
	}
	copyInput := map[string]any{}
	for key, value := range input {
		copyInput[key] = value
	}
	copyInput["now"] = m.now().Format(time.RFC3339Nano)
	if err := m.Protocol.Evaluate("migration", copyInput); err != nil {
		return err
	}
	proof := um(copyInput["proof"])
	legacy, unified := um(proof["legacyEnvelope"]), um(proof["unifiedEnvelope"])
	if unified["tenantId"] != spec.GetTenantId() {
		return fmt.Errorf("migration tenant mismatch")
	}
	s, root, _, err := m.load(id)
	if err != nil {
		return err
	}
	if s.Tenant != "" && s.Tenant != spec.GetTenantId() {
		return fmt.Errorf("migration identity mismatch")
	}
	if s.Migrations == nil {
		s.Migrations = map[string]map[string]any{}
	}
	key := spec.GetTenantId() + "/" + us(unified["packageId"])
	a, digest := um(legacy["statement"])["digest"], um(unified["statement"])["manifestDigest"]
	marker := um(proof["marker"])
	facts := map[string]any{"activatedAt": proof["activatedAt"], "marker": marker, "versionId": unified["versionId"], "releaseSequence": unified["releaseSequence"], "legacyDigest": a, "manifestDigest": digest}
	mapping := key + "/" + us(unified["versionId"])
	if previous, ok := s.Migrations[mapping]; ok && !uequal(previous, facts) {
		return fmt.Errorf("immutable migration proof conflict")
	}
	for existingKey, previous := range s.Migrations {
		if len(existingKey) > len(key) && existingKey[:len(key)+1] == key+"/" && previous["activatedAt"] != proof["activatedAt"] {
			return fmt.Errorf("migration activation time changed")
		}
	}
	// Keep original signed bytes available for independent revalidation.
	archive, err := base64.StdEncoding.DecodeString(us(copyInput["rawArchiveBase64"]))
	if err != nil {
		return err
	}
	if err := unifiedAtomicBytes(filepath.Join(root, "migration-evidence", us(unified["versionId"]), "legacy.archive"), archive); err != nil {
		return err
	}
	if err := unifiedAtomicBytes(filepath.Join(root, "migration-evidence", us(unified["versionId"]), "manifest.json"), []byte(us(copyInput["rawManifest"]))); err != nil {
		return err
	}
	if err := unifiedAtomic(filepath.Join(root, "migration-evidence", us(unified["versionId"]), "proof.json"), proof); err != nil {
		return err
	}
	if watermark, ok := s.Watermarks[key]; ok {
		if un(watermark["releaseSequence"]) == un(unified["releaseSequence"]) {
			if watermark["versionId"] != unified["versionId"] {
				return fmt.Errorf("migration watermark version mismatch")
			}
			aliases := ua(watermark["verifiedDigestAliases"])
			if !ucontains(aliases, a) && !ucontains(aliases, digest) {
				return fmt.Errorf("migration aliases do not match prior bytes")
			}
			if !ucontains(aliases, a) {
				aliases = append(aliases, a)
			}
			if !ucontains(aliases, digest) {
				aliases = append(aliases, digest)
			}
			watermark["verifiedDigestAliases"] = aliases
		}
	} else {
		s.Watermarks[key] = map[string]any{"tenantId": spec.GetTenantId(), "packageId": unified["packageId"], "versionId": unified["versionId"], "releaseSequence": unified["releaseSequence"], "verifiedDigestAliases": []any{a, digest}}
	}
	s.Migrations[mapping] = facts
	s.Tenant, s.Owner = spec.GetTenantId(), spec.GetOwnerId()
	return m.save(root, s)
}

func (m *UnifiedManager) authorizeDowngrade(spec *nodev1.InstanceSpec, s *unifiedNodeState, ref *resourcev1.UnifiedResourceRef, revision uint64, previous unifiedInstalled) (string, error) {
	key := spec.GetTenantId() + "/" + ref.GetIdentity().GetPackageId()
	high := s.Watermarks[key]
	if high == nil {
		return "", fmt.Errorf("downgrade without high water")
	}
	if previous.DesiredState == "ENABLED" && previous.InstallationRevision == revision && previous.AuthorizationID != "" {
		var before resourcev1.UnifiedResourceRef
		if proto.Unmarshal(previous.Ref, &before) == nil && proto.Equal(before.GetIdentity(), ref.GetIdentity()) && before.GetReleaseSequence() == ref.GetReleaseSequence() {
			return previous.AuthorizationID, nil
		}
	}
	identity, err := unifiedProto(ref.GetIdentity())
	if err != nil {
		return "", err
	}
	used := []any{}
	for _, id := range s.UsedAuthorizations {
		used = append(used, id)
	}
	for _, authorization := range spec.GetUnifiedDowngrades() {
		document, err := unifiedProto(authorization)
		if err != nil {
			continue
		}
		input := map[string]any{"authorization": document, "identity": identity, "instanceId": spec.GetInstanceId(), "fromReleaseSequence": high["releaseSequence"], "toReleaseSequence": json.Number(fmt.Sprint(ref.GetReleaseSequence())), "now": m.now().Format(time.RFC3339Nano), "usedAuthorizationIds": used}
		if m.Protocol.Evaluate("downgrade", input) == nil {
			return authorization.GetAuthorizationId(), nil
		}
	}
	return "", fmt.Errorf("resource rollback lacks exact independent authorization")
}

func (m *UnifiedManager) cleanupQuarantines(root, host string) error {
	entries, err := os.ReadDir(filepath.Join(root, "disable-receipts"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() {
			return fmt.Errorf("invalid isolation receipt")
		}
		var receipt struct {
			SchemaVersion      int    `json:"schemaVersion"`
			TenantID           string `json:"tenantId"`
			OwnerID            string `json:"ownerId"`
			HostID             string `json:"hostId"`
			ReceiptID          string `json:"receiptId"`
			Reason             string `json:"reason"`
			ProcessStopped     bool   `json:"processStopped"`
			ViewCleared        bool   `json:"viewCleared"`
			NewSessionRequired bool   `json:"newSessionRequired"`
			RecordedAt         string `json:"recordedAt"`
		}
		if err := unifiedRead(filepath.Join(root, "disable-receipts", entry.Name()), &receipt); err != nil {
			return err
		}
		if receipt.SchemaVersion != 1 || !uUUID.MatchString(receipt.ReceiptID) || !uUUID.MatchString(receipt.TenantID) || !uUUID.MatchString(receipt.OwnerID) || receipt.HostID != host || !receipt.ProcessStopped || !receipt.ViewCleared || entry.Name() != receipt.ReceiptID+".json" {
			return fmt.Errorf("isolation receipt ownership invalid")
		}
		quarantine := filepath.Join(root, "skills-active", "quarantine", receipt.ReceiptID)
		if err := storage.CheckNoSymlink(root, quarantine); err != nil {
			return err
		}
		if err := os.RemoveAll(quarantine); err != nil {
			return err
		}
	}
	return nil
}
