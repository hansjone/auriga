package projection

import (
	"slices"
	"time"

	api "github.com/hansjone/auriga/infra/gen/msgnetapi"
)

// ConfirmationFacts come from the consumer clock, a fresh random request and
// the authoritative material ledger, never from evidence.Readers itself.
type ConfirmationFacts struct {
	Root, Tenant, Realm, Challenge, RequiredPhase string
	Now                                           time.Time
	ExpectedSelectors                             map[string]string
}

func VerifyTransportConfirmation(e api.SignedTransportConfirmation, b api.SignedTransportTrustBundle, a api.SignedRuntimeAuthority, c api.SignedRootControl, f ConfirmationFacts) error {
	for _, s := range []struct {
		v           any
		auth        api.Signature
		domain, key string
	}{
		{c.Payload, c.RootAuth, "Auriga.NetworkRootControl.v2", f.Root},
		{a.Payload, a.RootAuth, "Auriga.NetworkRuntimeAuthority.v2", f.Root},
		{b.Payload, b.RuntimeAuth, "Auriga.NetworkTransportTrustBundle.v2", a.Payload.PublicKeySpki},
		{e.Payload, e.RuntimeAuth, "Auriga.NetworkTransportConfirmation.v2", a.Payload.PublicKeySpki},
	} {
		if err := VerifySignature(s.v, s.auth, s.domain, s.key); err != nil {
			return err
		}
	}
	if !allowed(c.Payload, a) {
		return Fault("authority_unknown")
	}
	if slices.Contains(c.Payload.RevokedAuthorityIds, a.Payload.AuthorityId) {
		return Fault("authority_revoked")
	}
	if !slices.Contains(a.Payload.Capabilities, "network_transport") {
		return Fault("scope_denied")
	}
	if stamp(a.Payload.NotBefore).After(f.Now) {
		return Fault("scope_denied")
	}
	if a.Payload.TenantId != f.Tenant || b.Payload.TenantId != f.Tenant || c.Payload.TenantId != f.Tenant || a.Payload.Realm != f.Realm || b.Payload.Realm != f.Realm || c.Payload.Realm != f.Realm {
		return Fault("scope_denied")
	}
	ad, _ := ObjectDigest(a)
	bd, _ := ObjectDigest(b)
	p := e.Payload
	if b.Payload.AuthorityId != a.Payload.AuthorityId || b.Payload.AuthorityDigest != ad || b.RuntimeAuth.Kid != a.Payload.RuntimeKid || e.RuntimeAuth.Kid != a.Payload.RuntimeKid || p.AuthorityId != a.Payload.AuthorityId || p.AuthorityDigest != ad {
		return Fault("invalid_chain")
	}
	if p.ControlRevision != c.Payload.ControlRevision {
		return Fault("control_required")
	}
	if lt(c.Payload.ControlRevision, b.Payload.ControlRevision) {
		return Fault("control_required")
	}
	if p.BundleDigest != bd || p.TenantId != b.Payload.TenantId || p.Realm != b.Payload.Realm || p.BundleVersion != b.Payload.BundleVersion || p.Phase != b.Payload.Phase || !Equal(p.RequiredReaderIds, b.Payload.RequiredReaderIds) || p.Challenge != f.Challenge {
		return Fault("invalid_chain")
	}
	if stamp(p.IssuedAt).After(f.Now) || f.Now.Sub(stamp(p.IssuedAt)) > time.Minute {
		return Fault("projection_pending")
	}
	if f.RequiredPhase != "loaded" && f.RequiredPhase != "handshake_verified" {
		return Fault("projection_pending")
	}
	rows := map[string]api.TransportReaderEvidence{}
	for _, r := range p.Readers {
		if _, ok := rows[r.ReaderId]; ok {
			return Fault("invalid_chain")
		}
		rows[r.ReaderId] = r
	}
	if len(rows) != len(b.Payload.RequiredReaderIds) {
		return Fault("projection_pending")
	}
	for _, id := range b.Payload.RequiredReaderIds {
		r, ok := rows[id]
		if !ok {
			return Fault("projection_pending")
		}
		if r.Phase == "isolated" {
			if !r.IsolationEnforced {
				return Fault("identity_quarantined")
			}
			continue
		}
		if r.Phase != "loaded" && r.Phase != "handshake_verified" {
			return Fault("projection_pending")
		}
		d, _ := ObjectDigest(r.Selector)
		if r.Selector.InstallationId != r.IdentityId || r.Selector.BundleDigest != bd || r.Selector.BundleVersion != b.Payload.BundleVersion || f.ExpectedSelectors[id] != d {
			return Fault("invalid_chain")
		}
		if f.RequiredPhase == "handshake_verified" && r.Phase != "handshake_verified" {
			return Fault("projection_pending")
		}
	}
	return nil
}

type TransportTransitionFacts struct {
	Root, Tenant, Realm                     string
	SeenGeneration, SeenVersion, SeenDigest string
	IsolationEnforced                       bool
}

func CheckTransportPredecessor(current, previous api.SignedTransportTrustBundle, required string) error {
	d, _ := ObjectDigest(previous)
	if d != current.Payload.PreviousBundleDigest {
		return Fault("invalid_chain")
	}
	phase, want := "dual_trust", "loaded"
	if current.Payload.Phase == "retire_old" {
		phase, want = "new_leaves", "handshake_verified"
	}
	if previous.Payload.Phase != phase {
		return Fault("invalid_transition")
	}
	if required != want {
		return Fault("projection_pending")
	}
	return nil
}

// VerifyTransportTransition verifies the root-authorized first dual-trust
// snapshot, including the old runtime's historical root chain.
func VerifyTransportTransition(proof api.TransportTransitionProof, f TransportTransitionFacts) error {
	c, a, t, q := proof.Control, proof.Authority, proof.FirstBundle, proof.Predecessor
	p := q.Bundle
	for _, s := range []struct {
		v           any
		auth        api.Signature
		domain, key string
	}{
		{c.Payload, c.RootAuth, "Auriga.NetworkRootControl.v2", f.Root},
		{a.Payload, a.RootAuth, "Auriga.NetworkRuntimeAuthority.v2", f.Root},
		{q.Control.Payload, q.Control.RootAuth, "Auriga.NetworkRootControl.v2", f.Root},
		{q.Authority.Payload, q.Authority.RootAuth, "Auriga.NetworkRuntimeAuthority.v2", f.Root},
		{p.Payload, p.RuntimeAuth, "Auriga.NetworkTransportTrustBundle.v2", q.Authority.Payload.PublicKeySpki},
		{t.Payload, t.RuntimeAuth, "Auriga.NetworkTransportTrustBundle.v2", a.Payload.PublicKeySpki},
	} {
		if e := VerifySignature(s.v, s.auth, s.domain, s.key); e != nil {
			return e
		}
	}
	if c.Payload.TenantId != f.Tenant || a.Payload.TenantId != f.Tenant || t.Payload.TenantId != f.Tenant || p.Payload.TenantId != f.Tenant || q.Control.Payload.TenantId != f.Tenant || q.Authority.Payload.TenantId != f.Tenant || c.Payload.Realm != f.Realm || a.Payload.Realm != f.Realm || t.Payload.Realm != f.Realm || p.Payload.Realm != f.Realm || q.Control.Payload.Realm != f.Realm || q.Authority.Payload.Realm != f.Realm {
		return Fault("scope_denied")
	}
	ad, _ := ObjectDigest(a)
	qad, _ := ObjectDigest(q.Authority)
	pd, _ := ObjectDigest(p)
	if t.Payload.AuthorityId != a.Payload.AuthorityId || t.Payload.AuthorityDigest != ad || t.RuntimeAuth.Kid != a.Payload.RuntimeKid || p.Payload.AuthorityId != q.Authority.Payload.AuthorityId || p.Payload.AuthorityDigest != qad || p.RuntimeAuth.Kid != q.Authority.Payload.RuntimeKid {
		return Fault("invalid_chain")
	}
	if !allowed(q.Control.Payload, q.Authority) {
		return Fault("authority_unknown")
	}
	if slices.Contains(q.Control.Payload.RevokedAuthorityIds, p.Payload.AuthorityId) {
		return Fault("authority_revoked")
	}
	if !slices.Contains(q.Authority.Payload.Capabilities, "network_transport") || stamp(q.Authority.Payload.NotBefore).After(stamp(p.Payload.IssuedAt)) || lt(q.Control.Payload.ControlRevision, p.Payload.ControlRevision) {
		return Fault("invalid_chain")
	}
	if !allowed(c.Payload, a) {
		return Fault("authority_unknown")
	}
	if slices.Contains(c.Payload.RevokedAuthorityIds, t.Payload.AuthorityId) {
		return Fault("authority_revoked")
	}
	if lt(c.Payload.ControlRevision, t.Payload.ControlRevision) {
		return Fault("control_required")
	}
	if !slices.Contains(a.Payload.Capabilities, "network_transport") || stamp(a.Payload.NotBefore).After(stamp(t.Payload.IssuedAt)) {
		return Fault("scope_denied")
	}
	for _, s := range t.Payload.Scope {
		if !slices.ContainsFunc(a.Payload.TransportScope, func(x api.TransportScope) bool { return Equal(s, x) }) {
			return Fault("scope_denied")
		}
	}
	if lt(t.Payload.Generation, c.Payload.MinimumGeneration) {
		return Fault("version_rollback")
	}
	if t.Payload.PreviousBundleDigest != pd {
		return Fault("invalid_transition")
	}
	if f.SeenDigest != "" && f.SeenGeneration == p.Payload.Generation && f.SeenVersion == p.Payload.BundleVersion && f.SeenDigest != pd {
		return Fault("invalid_transition")
	}
	if (f.SeenGeneration == p.Payload.Generation && lt(p.Payload.BundleVersion, f.SeenVersion)) || lt(t.Payload.Generation, f.SeenGeneration) {
		return Fault("version_rollback")
	}
	x := c.Payload.TransportTransition
	if x == nil {
		return Fault("control_required")
	}
	if x.PredecessorDigest != pd || x.FromGeneration != p.Payload.Generation || x.ToGeneration != t.Payload.Generation || x.AuthorityId != t.Payload.AuthorityId || x.AuthorityDigest != ad || x.FirstBundleVersion != t.Payload.BundleVersion || t.Payload.Phase != "dual_trust" {
		return Fault("invalid_transition")
	}
	if x.Mode == "authority_transition" {
		if t.Payload.Generation != p.Payload.Generation {
			return Fault("invalid_transition")
		}
		if !lt(p.Payload.BundleVersion, t.Payload.BundleVersion) {
			return Fault("version_rollback")
		}
		if p.Payload.Phase != "retire_old" {
			return Fault("invalid_transition")
		}
		if slices.Contains(c.Payload.RevokedAuthorityIds, p.Payload.AuthorityId) {
			return Fault("authority_revoked")
		}
		if !allowed(c.Payload, q.Authority) {
			return Fault("authority_unknown")
		}
		if !Equal(t.Payload.Scope, p.Payload.Scope) {
			return Fault("scope_denied")
		}
		if !Equal(t.Payload.RequiredReaderIds, p.Payload.RequiredReaderIds) {
			return Fault("invalid_transition")
		}
		for _, old := range p.Payload.Cas {
			if !slices.ContainsFunc(t.Payload.Cas, func(n api.TransportCa) bool { return Equal(old, n) }) {
				return Fault("invalid_transition")
			}
		}
		if !slices.ContainsFunc(t.Payload.Cas, func(n api.TransportCa) bool {
			return !slices.ContainsFunc(p.Payload.Cas, func(old api.TransportCa) bool { return old.CaId == n.CaId })
		}) {
			return Fault("invalid_transition")
		}
	} else {
		if !lt(p.Payload.Generation, t.Payload.Generation) {
			return Fault("version_rollback")
		}
		if !slices.Contains(c.Payload.RevokedAuthorityIds, p.Payload.AuthorityId) {
			return Fault("authority_revoked")
		}
		for _, n := range t.Payload.Cas {
			for _, old := range p.Payload.Cas {
				if old.CaId == n.CaId || old.CertificateDer == n.CertificateDer {
					return Fault("invalid_transition")
				}
			}
		}
		if !f.IsolationEnforced {
			return Fault("identity_quarantined")
		}
	}
	return nil
}

// VerifyTransportCatchup verifies the immutable root anchor and the current
// snapshot separately. Historical controls never replace the latest control.
func VerifyTransportCatchup(proof api.TransportTransitionProof, current api.SignedTransportTrustBundle, control api.SignedRootControl, f TransportTransitionFacts, seenVersion, seenDigest string, confirmation func() error) error {
	if e := VerifyTransportTransition(proof, f); e != nil {
		return e
	}
	a, start := proof.Authority, proof.FirstBundle.Payload
	if e := VerifySignature(control.Payload, control.RootAuth, "Auriga.NetworkRootControl.v2", f.Root); e != nil {
		return e
	}
	if e := VerifySignature(current.Payload, current.RuntimeAuth, "Auriga.NetworkTransportTrustBundle.v2", a.Payload.PublicKeySpki); e != nil {
		return e
	}
	c, t := control.Payload, current.Payload
	if c.TenantId != f.Tenant || t.TenantId != f.Tenant || c.Realm != f.Realm || t.Realm != f.Realm {
		return Fault("scope_denied")
	}
	if lt(c.ControlRevision, proof.Control.Payload.ControlRevision) {
		return Fault("control_rollback")
	}
	if slices.Contains(c.RevokedAuthorityIds, a.Payload.AuthorityId) {
		return Fault("authority_revoked")
	}
	if !allowed(c, a) {
		return Fault("authority_unknown")
	}
	if t.AuthorityId != start.AuthorityId || t.AuthorityDigest != start.AuthorityDigest || t.Generation != start.Generation || current.RuntimeAuth.Kid != a.Payload.RuntimeKid {
		return Fault("invalid_chain")
	}
	if lt(c.ControlRevision, t.ControlRevision) {
		return Fault("control_required")
	}
	if lt(t.Generation, c.MinimumGeneration) || lt(t.BundleVersion, start.BundleVersion) || lt(t.BundleVersion, seenVersion) {
		return Fault("version_rollback")
	}
	d, _ := ObjectDigest(current)
	if t.BundleVersion == seenVersion && d != seenDigest {
		return Fault("same_version_conflict")
	}
	for _, s := range t.Scope {
		if !slices.ContainsFunc(a.Payload.TransportScope, func(x api.TransportScope) bool { return Equal(s, x) }) {
			return Fault("scope_denied")
		}
	}
	if t.Phase != "dual_trust" {
		if confirmation == nil {
			return Fault("projection_pending")
		}
		return confirmation()
	}
	return nil
}

// BindNodeControl checks only the independent enrolled node identity. It never
// grants projection material access or treats the ECDSA mTLS key as Ed25519.
func BindNodeControl(pin api.NodeControlPin, node, target, identity, revision string, compromised, privateMaterial, enrolled bool) error {
	if node != pin.NodeId || target != pin.TargetId || identity != pin.IdentityId {
		return Fault("scope_denied")
	}
	if revision != pin.IdentityRevision {
		return Fault("version_rollback")
	}
	if compromised {
		return Fault("identity_quarantined")
	}
	if privateMaterial {
		return Fault("scope_denied")
	}
	if !enrolled {
		return Fault("identity_quarantined")
	}
	_, e := public(pin.SigningSpki)
	return e
}

// CheckProjectionAccess also serves the installer's preflight. Mount isolation
// and dropped capabilities must come from its inspected runtime, not wire data.
func CheckProjectionAccess(component string, projector, reader, peer, owner uint32, dirMode, fileMode uint32, privateMount, capDrop bool) error {
	uid := readerUID(component)
	if (component != "network" && component != "broker" && component != "edge") || projector != uid || reader != uid || peer != uid || owner != uid || dirMode != 0700 || fileMode != 0600 || !privateMount || !capDrop {
		return Fault("scope_denied")
	}
	return nil
}
