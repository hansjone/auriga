package msgnet

import (
	"context"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"slices"
	"time"

	api "github.com/hansjone/auriga/infra/gen/msgnetapi"
)

// TransportSource exposes only frozen public objects. Implementations use a
// fixed internal mTLS/JWT peer; node agents never receive that credential.
type TransportSource interface {
	Predecessor(context.Context, string) (api.TransportPredecessor, error)
	Transition(context.Context, string) (api.TransportTransitionProof, error)
	Confirmation(context.Context, api.TransportConfirmationQuery) (api.SignedTransportConfirmation, error)
}

// TransportState is a private reader ledger, not a wire model. Removed CA IDs
// and certificates remain as tombstones across cycles, generations and restarts.
type TransportState struct {
	Control   *api.SignedRootControl          `json:"control,omitempty"`
	Bundle    *api.SignedTransportTrustBundle `json:"bundle,omitempty"`
	Authority *api.SignedRuntimeAuthority     `json:"authority,omitempty"`
	CAs       map[string]api.TransportCa      `json:"cas"`
	Retired   map[string]bool                 `json:"retired"`
}

type TransportConsumer struct {
	Schemas *Schemas
	Domain  TransportDomain
	RootKid string
	State   TransportState
	Now     func() time.Time
	// Save must atomically replace and fsync the reader's private ledger. A save
	// failure never authorizes the in-memory state or emits a loaded receipt.
	Save   func(TransportState) error
	Source TransportSource
}

func (t *TransportConsumer) CheckCurrent(identity string) error {
	if t.State.Control == nil || t.State.Bundle == nil || t.State.Authority == nil {
		return Fault("projection_pending")
	}
	if slices.Contains(t.State.Control.Payload.RevokedInstallationIds, identity) {
		return Fault("authority_revoked")
	}
	return t.bundle(*t.State.Bundle, *t.State.Authority)
}

func (t *TransportConsumer) now() time.Time {
	if t.Now != nil {
		return t.Now()
	}
	return time.Now()
}
func (t *TransportConsumer) model(name string, v any, out any) error {
	if t.Schemas == nil || t.RootKid == "" || t.Save == nil {
		return Fault("invalid_chain")
	}
	raw, e := Bytes(v)
	if e != nil {
		return e
	}
	return t.Schemas.Decode(name, raw, out)
}
func (t *TransportConsumer) root(c api.SignedRootControl) error {
	if c.RootAuth.Kid != t.RootKid || c.Payload.TenantId != t.Domain.Tenant || c.Payload.Realm != t.Domain.Realm {
		return Fault("scope_denied")
	}
	return signedRoot(c, t.Domain.RootSPKI)
}
func (t *TransportConsumer) authority(a api.SignedRuntimeAuthority, c api.SignedRootControl) error {
	if a.RootAuth.Kid != t.RootKid || a.Payload.TenantId != t.Domain.Tenant || a.Payload.Realm != t.Domain.Realm || a.Payload.ExpiryPolicy != "no_hard_expiry" || stamp(a.Payload.NotBefore).After(t.now()) || !slices.Contains(a.Payload.Capabilities, "network_transport") {
		return Fault("scope_denied")
	}
	if e := signedAuthority(a, t.Domain.RootSPKI); e != nil {
		return e
	}
	if !allowed(c.Payload, a) {
		return Fault("authority_unknown")
	}
	if slices.Contains(c.Payload.RevokedAuthorityIds, a.Payload.AuthorityId) {
		return Fault("authority_revoked")
	}
	return nil
}

// UpdateControl is independent from material loading. It must run before a
// cached TLS snapshot may authorize another connection, including failed reloads.
func (t *TransportConsumer) UpdateControl(c api.SignedRootControl) error {
	var checked api.SignedRootControl
	if e := t.model("SignedRootControl", c, &checked); e != nil {
		return e
	}
	if e := t.root(c); e != nil {
		return e
	}
	if old := t.State.Control; old != nil {
		if lt(c.Payload.ControlRevision, old.Payload.ControlRevision) || lt(c.Payload.MinimumGeneration, old.Payload.MinimumGeneration) || c.Payload.MinimumGeneration == old.Payload.MinimumGeneration && lt(c.Payload.MinimumKeysetVersion, old.Payload.MinimumKeysetVersion) {
			return Fault("control_rollback")
		}
		if c.Payload.ControlRevision == old.Payload.ControlRevision {
			if !Equal(c, *old) {
				return Fault("same_version_conflict")
			}
			return nil
		}
		for _, pair := range [][2][]string{{old.Payload.RevokedAuthorityIds, c.Payload.RevokedAuthorityIds}, {old.Payload.RevokedInstallationIds, c.Payload.RevokedInstallationIds}} {
			for _, id := range pair[0] {
				if !slices.Contains(pair[1], id) {
					return Fault("control_rollback")
				}
			}
		}
	}
	next := t.State
	next.Control = &c
	if e := t.Save(next); e != nil {
		return e
	}
	t.State = next
	return nil
}

func (t *TransportConsumer) bundle(b api.SignedTransportTrustBundle, a api.SignedRuntimeAuthority) error {
	if t.State.Control == nil {
		return Fault("control_required")
	}
	c := *t.State.Control
	if e := t.authority(a, c); e != nil {
		return e
	}
	if e := signedBundle(b, a); e != nil {
		return e
	}
	ad, _ := ObjectDigest(a)
	if b.RuntimeAuth.Kid != a.Payload.RuntimeKid || b.Payload.AuthorityId != a.Payload.AuthorityId || b.Payload.AuthorityDigest != ad || b.Payload.TenantId != t.Domain.Tenant || b.Payload.Realm != t.Domain.Realm || lt(c.Payload.ControlRevision, b.Payload.ControlRevision) || lt(b.Payload.Generation, c.Payload.MinimumGeneration) || stamp(b.Payload.IssuedAt).After(t.now().Add(time.Minute)) || stamp(b.Payload.IssuedAt).Before(stamp(a.Payload.NotBefore)) {
		return Fault("scope_denied")
	}
	for _, s := range b.Payload.Scope {
		if !slices.ContainsFunc(a.Payload.TransportScope, func(grant api.TransportScope) bool { return Equal(grant, s) }) {
			return Fault("scope_denied")
		}
	}
	for _, ca := range b.Payload.Cas {
		der, e := binaryText(ca.CertificateDer)
		if e != nil {
			return e
		}
		cert, e := x509.ParseCertificate(der)
		if e != nil || !cert.IsCA || !cert.BasicConstraintsValid || cert.KeyUsage&x509.KeyUsageCertSign == 0 || cert.CheckSignatureFrom(cert) != nil || cert.NotBefore.UTC().Format(time.RFC3339) != ca.NotBefore || cert.NotAfter.UTC().Format(time.RFC3339) != ca.NotAfter || !cert.NotAfter.After(cert.NotBefore) || cert.NotAfter.After(cert.NotBefore.AddDate(10, 0, 0)) {
			return Fault("invalid_chain")
		}
	}
	return nil
}

func (t *TransportConsumer) stage(ctx context.Context, b api.SignedTransportTrustBundle, a api.SignedRuntimeAuthority) error {
	if b.Payload.Phase == "dual_trust" {
		return nil
	}
	if t.Source == nil {
		return Fault("projection_pending")
	}
	p, e := t.Source.Predecessor(ctx, b.Payload.PreviousBundleDigest)
	if e != nil {
		return e
	}
	var checked api.TransportPredecessor
	if e = t.model("TransportPredecessor", p, &checked); e != nil {
		return e
	}
	if e = t.root(p.Control); e != nil {
		return e
	}
	if e = t.bundle(p.Bundle, p.Authority); e != nil {
		return e
	}
	pd, _ := ObjectDigest(p.Bundle)
	if pd != b.Payload.PreviousBundleDigest || p.Bundle.Payload.Generation != b.Payload.Generation || p.Bundle.Payload.AuthorityDigest != b.Payload.AuthorityDigest || !lt(p.Bundle.Payload.BundleVersion, b.Payload.BundleVersion) || !Equal(p.Bundle.Payload.Scope, b.Payload.Scope) || !Equal(p.Bundle.Payload.RequiredReaderIds, b.Payload.RequiredReaderIds) {
		return Fault("invalid_chain")
	}
	phase, level := "dual_trust", "loaded"
	if b.Payload.Phase == "retire_old" {
		phase, level = "new_leaves", "handshake_verified"
	}
	if p.Bundle.Payload.Phase != phase {
		return Fault("invalid_transition")
	}
	if phase == "dual_trust" && !Equal(b.Payload.Cas, p.Bundle.Payload.Cas) {
		return Fault("invalid_transition")
	}
	if phase == "new_leaves" {
		for _, ca := range b.Payload.Cas {
			if !slices.ContainsFunc(p.Bundle.Payload.Cas, func(old api.TransportCa) bool { return Equal(old, ca) }) {
				return Fault("invalid_transition")
			}
		}
	}
	challenge := make([]byte, 32)
	if _, e = rand.Read(challenge); e != nil {
		return e
	}
	q := api.TransportConfirmationQuery{TenantId: t.Domain.Tenant, Realm: t.Domain.Realm, BundleVersion: p.Bundle.Payload.BundleVersion, BundleDigest: pd, Challenge: base64.RawURLEncoding.EncodeToString(challenge)}
	evidence, e := t.Source.Confirmation(ctx, q)
	if e != nil {
		return e
	}
	var check api.SignedTransportConfirmation
	if e = t.model("SignedTransportConfirmation", evidence, &check); e != nil {
		return e
	}
	return VerifyTransportAttestation(evidence, p.Bundle, a, *t.State.Control, ConfirmationFacts{Domain: t.Domain, Now: t.now(), Challenge: q.Challenge, RequiredPhase: level})
}

// Accept verifies production transport state, obtains fresh globally signed
// phase evidence, preserves CA tombstones, then durably advances the fence.
// IsolationEnforced is supplied by a real local disconnect/deny mechanism.
func (t *TransportConsumer) Accept(ctx context.Context, b api.SignedTransportTrustBundle, a api.SignedRuntimeAuthority, isolationEnforced bool) error {
	var checked api.SignedTransportTrustBundle
	var authority api.SignedRuntimeAuthority
	if e := t.model("SignedTransportTrustBundle", b, &checked); e != nil {
		return e
	}
	if e := t.model("SignedRuntimeAuthority", a, &authority); e != nil {
		return e
	}
	if e := t.bundle(b, a); e != nil {
		return e
	}
	digest, _ := ObjectDigest(b)
	old := t.State.Bundle
	history := TransportHistory{IsolationEnforced: isolationEnforced}
	if old != nil {
		history.Generation, history.Version = old.Payload.Generation, old.Payload.BundleVersion
		history.Digest, _ = ObjectDigest(*old)
		if lt(b.Payload.Generation, history.Generation) || b.Payload.Generation == history.Generation && lt(b.Payload.BundleVersion, history.Version) {
			return Fault("version_rollback")
		}
		if b.Payload.Generation == history.Generation && b.Payload.BundleVersion == history.Version {
			if digest != history.Digest {
				return Fault("same_version_conflict")
			}
			return nil
		}
	}
	transition := old != nil && (old.Payload.AuthorityDigest != b.Payload.AuthorityDigest || old.Payload.Generation != b.Payload.Generation)
	anchor := t.State.Control.Payload.TransportTransition
	if transition || old == nil && anchor != nil && anchor.AuthorityDigest == b.Payload.AuthorityDigest {
		if t.Source == nil {
			return Fault("control_required")
		}
		proof, e := t.Source.Transition(ctx, b.Payload.AuthorityDigest)
		if e != nil {
			return e
		}
		var checkedProof api.TransportTransitionProof
		if e = t.model("TransportTransitionProof", proof, &checkedProof); e != nil {
			return e
		}
		if e = t.root(proof.Control); e != nil {
			return e
		}
		if e = t.root(proof.Predecessor.Control); e != nil {
			return e
		}
		if proof.Authority.RootAuth.Kid != t.RootKid || proof.Predecessor.Authority.RootAuth.Kid != t.RootKid {
			return Fault("scope_denied")
		}
		if e = VerifyTransportTransition(proof, t.Domain, history); e != nil {
			return e
		}
		if !Equal(proof.Authority, a) || b.Payload.Generation != proof.FirstBundle.Payload.Generation || lt(b.Payload.BundleVersion, proof.FirstBundle.Payload.BundleVersion) || lt(t.State.Control.Payload.ControlRevision, proof.Control.Payload.ControlRevision) {
			return Fault("invalid_chain")
		}
		if b.Payload.BundleVersion == proof.FirstBundle.Payload.BundleVersion && !Equal(b, proof.FirstBundle) {
			return Fault("same_version_conflict")
		}
	} else if b.Payload.Phase == "dual_trust" && old != nil {
		if t.Source == nil {
			return Fault("projection_pending")
		}
		p, e := t.Source.Predecessor(ctx, b.Payload.PreviousBundleDigest)
		if e != nil {
			return e
		}
		var checkedPrev api.TransportPredecessor
		if e = t.model("TransportPredecessor", p, &checkedPrev); e != nil {
			return e
		}
		if e = t.bundle(p.Bundle, p.Authority); e != nil {
			return e
		}
		pd, _ := ObjectDigest(p.Bundle)
		if pd != b.Payload.PreviousBundleDigest || p.Bundle.Payload.AuthorityDigest != b.Payload.AuthorityDigest || p.Bundle.Payload.Generation != b.Payload.Generation || p.Bundle.Payload.Phase != "retire_old" || lt(p.Bundle.Payload.BundleVersion, history.Version) || !Equal(p.Bundle.Payload.Scope, b.Payload.Scope) || !Equal(p.Bundle.Payload.RequiredReaderIds, b.Payload.RequiredReaderIds) || len(b.Payload.Cas) <= len(p.Bundle.Payload.Cas) {
			return Fault("invalid_transition")
		}
		for _, ca := range p.Bundle.Payload.Cas {
			if !slices.ContainsFunc(b.Payload.Cas, func(v api.TransportCa) bool { return Equal(v, ca) }) {
				return Fault("invalid_transition")
			}
		}
	}
	if e := t.stage(ctx, b, a); e != nil {
		return e
	}
	next := t.State
	next.Bundle, next.Authority = &b, &a
	next.CAs = map[string]api.TransportCa{}
	next.Retired = map[string]bool{}
	for id, ca := range t.State.CAs {
		next.CAs[id] = ca
	}
	for id, v := range t.State.Retired {
		next.Retired[id] = v
	}
	for _, ca := range b.Payload.Cas {
		if previous, ok := next.CAs[ca.CaId]; ok && !Equal(previous, ca) || next.Retired[ca.CaId] {
			return Fault("invalid_transition")
		}
		for id, previous := range next.CAs {
			if id != ca.CaId && previous.CertificateDer == ca.CertificateDer {
				return Fault("invalid_transition")
			}
		}
		next.CAs[ca.CaId] = ca
	}
	for id := range next.CAs {
		if !slices.ContainsFunc(b.Payload.Cas, func(c api.TransportCa) bool { return c.CaId == id }) {
			next.Retired[id] = true
		}
	}
	if e := t.Save(next); e != nil {
		return e
	}
	t.State = next
	return nil
}
