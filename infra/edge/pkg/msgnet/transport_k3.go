package msgnet

import (
	"slices"
	"time"

	api "github.com/hansjone/auriga/infra/gen/msgnetapi"
)

// TransportHistory comes from the durable local ledger, never the received proof.
type TransportHistory struct {
	Generation, Version, Digest string
	IsolationEnforced           bool
}

type TransportDomain struct{ RootSPKI, Tenant, Realm string }

func signedRoot(v api.SignedRootControl, root string) error {
	return VerifySignature(v.Payload, v.RootAuth, "Auriga.NetworkRootControl.v2", root)
}
func signedAuthority(v api.SignedRuntimeAuthority, root string) error {
	return VerifySignature(v.Payload, v.RootAuth, "Auriga.NetworkRuntimeAuthority.v2", root)
}
func signedBundle(v api.SignedTransportTrustBundle, a api.SignedRuntimeAuthority) error {
	return VerifySignature(v.Payload, v.RuntimeAuth, "Auriga.NetworkTransportTrustBundle.v2", a.Payload.PublicKeySpki)
}

// VerifyTransportTransition validates the exact root-authorized starting point.
// Historical authorization proves the predecessor; only latest authorizes the new bundle.
func VerifyTransportTransition(proof api.TransportTransitionProof, domain TransportDomain, seen TransportHistory) error {
	c, a := proof.Control.Payload, proof.Authority.Payload
	p, current := proof.Predecessor.Bundle.Payload, proof.FirstBundle.Payload
	oldAuthority, oldControl := proof.Predecessor.Authority, proof.Predecessor.Control
	for _, verify := range []func() error{
		func() error { return signedRoot(proof.Control, domain.RootSPKI) },
		func() error { return signedAuthority(proof.Authority, domain.RootSPKI) },
		func() error { return signedRoot(oldControl, domain.RootSPKI) },
		func() error { return signedAuthority(oldAuthority, domain.RootSPKI) },
		func() error { return signedBundle(proof.Predecessor.Bundle, oldAuthority) },
		func() error { return signedBundle(proof.FirstBundle, proof.Authority) },
	} {
		if e := verify(); e != nil {
			return e
		}
	}
	for _, scope := range [][2]string{{c.TenantId, c.Realm}, {a.TenantId, a.Realm}, {p.TenantId, p.Realm}, {current.TenantId, current.Realm}, {oldControl.Payload.TenantId, oldControl.Payload.Realm}, {oldAuthority.Payload.TenantId, oldAuthority.Payload.Realm}} {
		if scope[0] != domain.Tenant || scope[1] != domain.Realm {
			return Fault("scope_denied")
		}
	}
	ad, _ := ObjectDigest(proof.Authority)
	od, _ := ObjectDigest(oldAuthority)
	if current.AuthorityId != a.AuthorityId || current.AuthorityDigest != ad || proof.FirstBundle.RuntimeAuth.Kid != a.RuntimeKid || p.AuthorityId != oldAuthority.Payload.AuthorityId || p.AuthorityDigest != od {
		return Fault("invalid_chain")
	}
	if !allowed(oldControl.Payload, oldAuthority) {
		return Fault("authority_unknown")
	}
	if lt(oldControl.Payload.ControlRevision, p.ControlRevision) || lt(p.Generation, oldControl.Payload.MinimumGeneration) {
		return Fault("control_required")
	}
	if slices.Contains(oldControl.Payload.RevokedAuthorityIds, p.AuthorityId) {
		return Fault("authority_revoked")
	}
	if !allowed(c, proof.Authority) {
		return Fault("authority_unknown")
	}
	if slices.Contains(c.RevokedAuthorityIds, current.AuthorityId) {
		return Fault("authority_revoked")
	}
	if lt(c.ControlRevision, current.ControlRevision) {
		return Fault("control_required")
	}
	if !slices.Contains(a.Capabilities, "network_transport") || stamp(a.NotBefore).After(stamp(current.IssuedAt)) {
		return Fault("scope_denied")
	}
	for _, scope := range current.Scope {
		if !slices.ContainsFunc(a.TransportScope, func(grant api.TransportScope) bool { return Equal(grant, scope) }) {
			return Fault("scope_denied")
		}
	}
	if lt(current.Generation, c.MinimumGeneration) {
		return Fault("version_rollback")
	}
	pd, _ := ObjectDigest(proof.Predecessor.Bundle)
	if pd != current.PreviousBundleDigest {
		return Fault("invalid_transition")
	}
	if seen.Digest != "" && seen.Generation == p.Generation && seen.Version == p.BundleVersion && seen.Digest != pd {
		return Fault("invalid_transition")
	}
	if seen.Version != "" && seen.Generation == p.Generation && lt(p.BundleVersion, seen.Version) || seen.Generation != "" && lt(current.Generation, seen.Generation) {
		return Fault("version_rollback")
	}
	anchor := c.TransportTransition
	if anchor == nil {
		return Fault("control_required")
	}
	if anchor.PredecessorDigest != pd || anchor.FromGeneration != p.Generation || anchor.ToGeneration != current.Generation || anchor.AuthorityId != current.AuthorityId || anchor.AuthorityDigest != current.AuthorityDigest || anchor.FirstBundleVersion != current.BundleVersion || current.Phase != "dual_trust" {
		return Fault("invalid_transition")
	}
	switch anchor.Mode {
	case "authority_transition":
		if current.Generation != p.Generation {
			return Fault("invalid_transition")
		}
		if !lt(p.BundleVersion, current.BundleVersion) {
			return Fault("version_rollback")
		}
		if p.Phase != "retire_old" {
			return Fault("invalid_transition")
		}
		if slices.Contains(c.RevokedAuthorityIds, p.AuthorityId) {
			return Fault("authority_revoked")
		}
		if !allowed(c, oldAuthority) {
			return Fault("authority_unknown")
		}
		if !Equal(current.Scope, p.Scope) {
			return Fault("scope_denied")
		}
		if !Equal(current.RequiredReaderIds, p.RequiredReaderIds) {
			return Fault("invalid_transition")
		}
		for _, old := range p.Cas {
			if !slices.ContainsFunc(current.Cas, func(ca api.TransportCa) bool { return Equal(ca, old) }) {
				return Fault("invalid_transition")
			}
		}
		if !slices.ContainsFunc(current.Cas, func(ca api.TransportCa) bool {
			return !slices.ContainsFunc(p.Cas, func(old api.TransportCa) bool { return old.CaId == ca.CaId })
		}) {
			return Fault("invalid_transition")
		}
	case "emergency_recovery":
		if !lt(p.Generation, current.Generation) {
			return Fault("version_rollback")
		}
		if !slices.Contains(c.RevokedAuthorityIds, p.AuthorityId) {
			return Fault("authority_revoked")
		}
		for _, ca := range current.Cas {
			if slices.ContainsFunc(p.Cas, func(old api.TransportCa) bool { return old.CaId == ca.CaId || old.CertificateDer == ca.CertificateDer }) {
				return Fault("invalid_transition")
			}
		}
		if !seen.IsolationEnforced {
			return Fault("identity_quarantined")
		}
	default:
		return Fault("invalid_transition")
	}
	return nil
}

// ConfirmationFacts must be obtained from the installed scope, fresh request,
// and authorized material ledger. ExpectedSelectors must not be copied from evidence.
type ConfirmationFacts struct {
	Domain                   TransportDomain
	Now                      time.Time
	Challenge, RequiredPhase string
	ExpectedSelectors        map[string]string
}

func VerifyTransportConfirmation(evidence api.SignedTransportConfirmation, bundle api.SignedTransportTrustBundle, authority api.SignedRuntimeAuthority, control api.SignedRootControl, f ConfirmationFacts) error {
	return verifyTransportConfirmation(evidence, bundle, authority, control, f, true)
}

// VerifyTransportAttestation is the remote consumer's trust boundary: the
// root-authorized runtime attests to C's PG authorization ledger. Only the
// issuer/auditor with independent access to that ledger can use the full
// ExpectedSelectors check above. Never reconstruct that map from evidence.
func VerifyTransportAttestation(evidence api.SignedTransportConfirmation, bundle api.SignedTransportTrustBundle, authority api.SignedRuntimeAuthority, control api.SignedRootControl, f ConfirmationFacts) error {
	return verifyTransportConfirmation(evidence, bundle, authority, control, f, false)
}

func verifyTransportConfirmation(evidence api.SignedTransportConfirmation, bundle api.SignedTransportTrustBundle, authority api.SignedRuntimeAuthority, control api.SignedRootControl, f ConfirmationFacts, audit bool) error {
	e, b, a, c := evidence.Payload, bundle.Payload, authority.Payload, control.Payload
	for _, verify := range []func() error{
		func() error { return signedRoot(control, f.Domain.RootSPKI) },
		func() error { return signedAuthority(authority, f.Domain.RootSPKI) },
		func() error { return signedBundle(bundle, authority) },
		func() error {
			return VerifySignature(e, evidence.RuntimeAuth, "Auriga.NetworkTransportConfirmation.v2", a.PublicKeySpki)
		},
	} {
		if err := verify(); err != nil {
			return err
		}
	}
	if !allowed(c, authority) {
		return Fault("authority_unknown")
	}
	if slices.Contains(c.RevokedAuthorityIds, a.AuthorityId) {
		return Fault("authority_revoked")
	}
	if !slices.Contains(a.Capabilities, "network_transport") {
		return Fault("scope_denied")
	}
	for _, scope := range [][2]string{{a.TenantId, a.Realm}, {b.TenantId, b.Realm}, {c.TenantId, c.Realm}} {
		if scope[0] != f.Domain.Tenant || scope[1] != f.Domain.Realm {
			return Fault("scope_denied")
		}
	}
	ad, _ := ObjectDigest(authority)
	bd, _ := ObjectDigest(bundle)
	if b.AuthorityId != a.AuthorityId || b.AuthorityDigest != ad || evidence.RuntimeAuth.Kid != a.RuntimeKid || e.AuthorityDigest != ad || e.AuthorityId != a.AuthorityId {
		return Fault("invalid_chain")
	}
	if e.ControlRevision != c.ControlRevision {
		return Fault("control_required")
	}
	if e.BundleDigest != bd || e.TenantId != b.TenantId || e.Realm != b.Realm || e.BundleVersion != b.BundleVersion || e.Phase != b.Phase || !Equal(e.RequiredReaderIds, b.RequiredReaderIds) || e.Challenge != f.Challenge {
		return Fault("invalid_chain")
	}
	issued := stamp(e.IssuedAt)
	if issued.After(f.Now) || f.Now.Sub(issued) > time.Minute {
		return Fault("projection_pending")
	}
	rows := make(map[string]api.TransportReaderEvidence, len(e.Readers))
	for _, row := range e.Readers {
		if _, duplicate := rows[row.ReaderId]; duplicate {
			return Fault("invalid_chain")
		}
		rows[row.ReaderId] = row
	}
	if len(rows) != len(b.RequiredReaderIds) {
		return Fault("projection_pending")
	}
	for _, id := range b.RequiredReaderIds {
		row, found := rows[id]
		if !found {
			return Fault("projection_pending")
		}
		if row.Phase == "isolated" {
			if !row.IsolationEnforced {
				return Fault("identity_quarantined")
			}
			continue
		}
		digest, _ := ObjectDigest(row.Selector)
		if CheckSelector(row.Selector) != nil || row.Selector.TenantId != e.TenantId || row.Selector.Realm != e.Realm || row.Selector.InstallationId != row.IdentityId || row.Selector.BundleDigest != e.BundleDigest || row.Selector.BundleVersion != e.BundleVersion || audit && f.ExpectedSelectors[id] != digest {
			return Fault("invalid_chain")
		}
		if f.RequiredPhase == "handshake_verified" && row.Phase != "handshake_verified" {
			return Fault("projection_pending")
		}
	}
	return nil
}

type StageConfirmation struct {
	Evidence  api.SignedTransportConfirmation
	Bundle    api.SignedTransportTrustBundle
	Authority api.SignedRuntimeAuthority
	Control   api.SignedRootControl
	Facts     ConfirmationFacts
}

func VerifyTransportCatchup(proof api.TransportTransitionProof, current api.SignedTransportTrustBundle, control api.SignedRootControl, domain TransportDomain, transitionHistory, currentHistory TransportHistory, confirmation *StageConfirmation) error {
	if e := VerifyTransportTransition(proof, domain, transitionHistory); e != nil {
		return e
	}
	start, now, c, a := proof.FirstBundle.Payload, current.Payload, control.Payload, proof.Authority.Payload
	if e := signedRoot(control, domain.RootSPKI); e != nil {
		return e
	}
	if e := signedBundle(current, proof.Authority); e != nil {
		return e
	}
	if c.TenantId != domain.Tenant || c.Realm != domain.Realm || now.TenantId != domain.Tenant || now.Realm != domain.Realm {
		return Fault("scope_denied")
	}
	if lt(c.ControlRevision, proof.Control.Payload.ControlRevision) {
		return Fault("control_rollback")
	}
	if slices.Contains(c.RevokedAuthorityIds, a.AuthorityId) {
		return Fault("authority_revoked")
	}
	if !allowed(c, proof.Authority) {
		return Fault("authority_unknown")
	}
	if now.AuthorityId != start.AuthorityId || now.AuthorityDigest != start.AuthorityDigest || now.Generation != start.Generation {
		return Fault("invalid_chain")
	}
	if lt(c.ControlRevision, now.ControlRevision) {
		return Fault("control_required")
	}
	if lt(now.Generation, c.MinimumGeneration) || lt(now.BundleVersion, start.BundleVersion) || currentHistory.Generation != "" && lt(now.Generation, currentHistory.Generation) || currentHistory.Version != "" && (currentHistory.Generation == "" || currentHistory.Generation == now.Generation) && lt(now.BundleVersion, currentHistory.Version) {
		return Fault("version_rollback")
	}
	cd, _ := ObjectDigest(current)
	if currentHistory.Version == now.BundleVersion && (currentHistory.Generation == "" || currentHistory.Generation == now.Generation) && cd != currentHistory.Digest {
		return Fault("same_version_conflict")
	}
	for _, scope := range now.Scope {
		if !slices.ContainsFunc(a.TransportScope, func(grant api.TransportScope) bool { return Equal(grant, scope) }) {
			return Fault("scope_denied")
		}
	}
	if now.Phase != "dual_trust" {
		if confirmation == nil || VerifyTransportConfirmation(confirmation.Evidence, confirmation.Bundle, confirmation.Authority, confirmation.Control, confirmation.Facts) != nil {
			return Fault("projection_pending")
		}
		previousDigest, _ := ObjectDigest(confirmation.Bundle)
		if previousDigest != now.PreviousBundleDigest {
			return Fault("invalid_chain")
		}
		phase, level := "dual_trust", "loaded"
		if now.Phase == "retire_old" {
			phase, level = "new_leaves", "handshake_verified"
		}
		if confirmation.Bundle.Payload.Phase != phase {
			return Fault("invalid_transition")
		}
		if confirmation.Facts.RequiredPhase != level {
			return Fault("projection_pending")
		}
	}
	return nil
}
