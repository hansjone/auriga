package msgnet

import (
	"encoding/json"
	"math"
	"slices"
	"time"

	api "github.com/hansjone/auriga/infra/gen/msgnetapi"
)

func CheckClock(pgSkew, wallDelta, monotonicDelta float64, first, clockError, stable bool) error {
	if math.Abs(pgSkew) > 60 || wallDelta-monotonicDelta > 300 || wallDelta-monotonicDelta < -60 || (first && clockError && !stable) {
		return Fault("clock_error")
	}
	return nil
}

func CheckConfig(s *Schemas, raw []byte) error {
	var c api.RenewalConfig
	if e := s.Decode("RenewalConfig", raw, &c); e != nil {
		return Fault("invalid_transition")
	}
	days := append([]int32(nil), c.ReviewReminderDays...)
	slices.Sort(days)
	slices.Reverse(days)
	if len(days) != 3 || days[0] < 30 || days[1] < 7 || days[2] < 1 || c.RefreshSeconds >= c.KeysetSeconds || int64(c.RotationSeconds)+int64(c.PreannounceSeconds) > int64(c.BusinessKeySeconds) || c.PreannounceSeconds < c.MaturitySeconds || c.LeafRenewBeforeDays >= c.LeafDays || c.CaRenewBeforeYears >= c.CaYears {
		return Fault("invalid_transition")
	}
	return nil
}
func VerifyAnnouncement(n api.SignedKeysetAnnouncementV2, p api.Publication, persisted bool) error {
	a, k := p.Authority.Payload, p.Keyset.Payload
	if e := VerifySignature(n.Payload, n.RuntimeAuth, "Auriga.NetworkKeysetAnnouncement.v2", a.PublicKeySpki); e != nil {
		return e
	}
	if !slices.Contains(a.Capabilities, "announcement") {
		return Fault("scope_denied")
	}
	ad, _ := ObjectDigest(p.Authority)
	if n.Payload.AuthorityDigest != ad || n.Payload.Realm != k.Realm || n.Payload.TenantId != k.TenantId {
		return Fault("scope_denied")
	}
	if !persisted {
		return Fault("projection_pending")
	}
	if lt(k.Generation, n.Payload.Generation) || (n.Payload.Generation == k.Generation && lt(k.KeysetVersion, n.Payload.KeysetVersion)) {
		return Fault("projection_pending")
	}
	kd, _ := ObjectDigest(p.Keyset)
	if n.Payload.Generation != k.Generation || n.Payload.KeysetVersion != k.KeysetVersion || n.Payload.KeysetDigest != kd || n.Payload.ValidUntil != k.ValidUntil {
		return Fault("invalid_chain")
	}
	return nil
}
func VerifyIdentity(v any, auth api.Signature, domain, spki string) error {
	b, e := Bytes(v)
	if e != nil {
		return e
	}
	var m map[string]json.RawMessage
	if e = json.Unmarshal(b, &m); e != nil {
		return e
	}
	delete(m, "identity_auth")
	return VerifySignature(m, auth, domain, spki)
}
func VerifyProjection(q api.ProjectionRequest, m api.ProjectionMaterial, spki string, used bool, exported []string) error {
	if e := VerifyIdentity(q, q.IdentityAuth, "Auriga.NetworkProjectionRequest.v2", spki); e != nil {
		return e
	}
	if q.TenantId != m.TenantId || q.Realm != m.Realm || q.InstallationId != m.InstallationId || q.Component != m.Component || q.Nonce != m.Nonce || used {
		return Fault("invalid_transition")
	}
	if q.ExpectedGeneration != m.Generation || q.ExpectedKeysetVersion != m.KeysetVersion {
		return Fault("version_rollback")
	}
	for _, purpose := range exported {
		if purpose != "protocol_response" {
			return Fault("scope_denied")
		}
	}
	if q.Component != "network" {
		return Fault("scope_denied")
	}
	return nil
}
func VerifyControlReceipt(q api.ControlPackage, r api.InstallationReceipt, spki, identity string, persisted, used, exact, compromised, ready bool) error {
	if e := VerifyIdentity(r, r.IdentityAuth, "Auriga.NetworkControlReceipt.v2", spki); e != nil {
		return e
	}
	if r.PackageId != q.PackageId || r.TenantId != q.TenantId || r.Realm != q.Realm || r.TargetId != q.TargetId || r.Challenge != q.Challenge || r.ControlDigest != q.ControlDigest {
		return Fault("invalid_transition")
	}
	if r.IdentityId != identity {
		return Fault("scope_denied")
	}
	if r.ControlRevision != q.Control.Payload.ControlRevision || r.Result != "installed" || !persisted || (used && !exact) {
		return Fault("invalid_transition")
	}
	if compromised || !ready {
		return Fault("identity_quarantined")
	}
	return nil
}
func VerifyMigration(p api.SignedMigrationPermit, root string, members []api.MigrationCommitmentMember, currentVersion, currentDigest, first string, frozen, remaining float64, barrier, disabled bool) error {
	if e := VerifySignature(p.Payload, p.RootAuth, "Auriga.NetworkMigrationPermit.v2", root); e != nil {
		return e
	}
	d, _ := ObjectDigest(members)
	if d != p.Payload.QueueCommitmentDigest || !slices.ContainsFunc(members, func(m api.MigrationCommitmentMember) bool {
		return m.Version == currentVersion && m.PairDigest == currentDigest
	}) || lt(p.Payload.MaximumCommittedV1Version, currentVersion) {
		return Fault("migration_outside_commitment")
	}
	if frozen > 600 || remaining < 21600 {
		return Fault("migration_expired")
	}
	if lt(first, p.Payload.MinimumV2Version) || (barrier && !disabled) {
		return Fault("invalid_transition")
	}
	return nil
}
func VerifyArchive(issued, before string, grace float64, removed, reappeared, tombstones []api.Tombstone) error {
	if stamp(before).After(stamp(issued).Add(-time.Duration(grace) * time.Second)) {
		return Fault("invalid_transition")
	}
	for _, key := range removed {
		if stamp(key.NotAfter).After(stamp(before)) {
			return Fault("invalid_transition")
		}
	}
	for _, key := range reappeared {
		for _, old := range tombstones {
			if old.Kid == key.Kid && (!Equal(old, key) || key.TerminalState == "active") {
				return Fault("invalid_transition")
			}
		}
	}
	return nil
}
