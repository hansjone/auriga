package projection

import (
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	api "github.com/hansjone/auriga/infra/gen/msgnetapi"
)

type ledger struct {
	Control           *api.SignedRootControl
	Publication       *api.Publication
	Bundle            *api.SignedTransportTrustBundle
	Keys              map[string]api.PublicKey
	First             map[string]api.FirstPublication
	ReaderPersisted   bool
	HandshakeVerified bool
	TransportCAs      map[string]transportCAHistory
}
type transportCAHistory struct {
	CA      api.TransportCa
	Retired bool
}
type Trust struct {
	mu                        sync.Mutex
	schemas                   *Schemas
	root, tenant, realm, path string
	pin                       api.InstallationPin
	state                     ledger
	now                       func() time.Time
	confirmTransport          func(api.Publication, api.SignedTransportTrustBundle, api.SignedRootControl, *api.SignedTransportTrustBundle, time.Time) error
}

// SetTransportConfirmation connects the authoritative C installation-receipt
// ledger. Local reader success is not proof
// that every required reader persisted the bundle or verified its handshakes.
func (t *Trust) SetTransportConfirmation(confirm func(api.Publication, api.SignedTransportTrustBundle, api.SignedRootControl, *api.SignedTransportTrustBundle, time.Time) error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.confirmTransport = confirm
}

func NewTrust(schemas *Schemas, root, tenant, realm, controlDir string, pin api.InstallationPin) (*Trust, error) {
	if schemas == nil {
		return nil, Fault("invalid_chain")
	}
	if e := validatePin(pin); e != nil {
		return nil, e
	}
	if _, e := public(root); e != nil {
		return nil, e
	}
	if e := directory(controlDir); e != nil {
		return nil, e
	}
	t := &Trust{schemas: schemas, root: root, tenant: tenant, realm: realm, path: filepath.Join(controlDir, "trust-ledger.json"), pin: pin, now: time.Now}
	t.state.Keys = map[string]api.PublicKey{}
	t.state.First = map[string]api.FirstPublication{}
	b, e := readPrivateFile(t.path, 128*MaxFile)
	if e == nil {
		if e = Decode(b, &t.state, true); e != nil {
			return nil, e
		}
		if t.state.Control == nil {
			return nil, Fault("invalid_chain")
		}
		if e = VerifySignature(t.state.Control.Payload, t.state.Control.RootAuth, "Auriga.NetworkRootControl.v2", root); e != nil {
			return nil, e
		}
	} else if !os.IsNotExist(e) {
		return nil, e
	}
	return t, nil
}

// NewTrustWithClock permits an injected, independently controlled clock without
// changing the host clock. Production callers normally use NewTrust.
func NewTrustWithClock(schemas *Schemas, root, tenant, realm, controlDir string, pin api.InstallationPin, now func() time.Time) (*Trust, error) {
	if now == nil {
		return nil, Fault("invalid_transition")
	}
	t, e := NewTrust(schemas, root, tenant, realm, controlDir, pin)
	if e == nil {
		t.now = now
	}
	return t, e
}
func (t *Trust) persist(next ledger) error {
	b, e := Bytes(next)
	if e != nil {
		return e
	}
	if e = atomicFile(t.path, b); e != nil {
		return e
	}
	t.state = next
	return nil
}
func (t *Trust) UpdateControl(c api.SignedRootControl) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.updateControl(c)
}
func (t *Trust) updateControl(c api.SignedRootControl) error {
	b, e := Bytes(c)
	if e != nil {
		return e
	}
	var checked api.SignedRootControl
	if e = t.schemas.Decode("SignedRootControl", b, &checked); e != nil {
		return e
	}
	if e = VerifySignature(c.Payload, c.RootAuth, "Auriga.NetworkRootControl.v2", t.root); e != nil {
		return e
	}
	if c.Payload.TenantId != t.tenant || c.Payload.Realm != t.realm {
		return Fault("scope_denied")
	}
	if old := t.state.Control; old != nil {
		if lt(c.Payload.ControlRevision, old.Payload.ControlRevision) {
			return Fault("control_rollback")
		}
		if c.Payload.ControlRevision == old.Payload.ControlRevision {
			if !Equal(c, *old) {
				return Fault("same_version_conflict")
			}
			return nil
		}
		for _, id := range old.Payload.RevokedAuthorityIds {
			if !slices.Contains(c.Payload.RevokedAuthorityIds, id) {
				return Fault("control_rollback")
			}
		}
		for _, id := range old.Payload.RevokedInstallationIds {
			if !slices.Contains(c.Payload.RevokedInstallationIds, id) {
				return Fault("control_rollback")
			}
		}
	}
	next := t.state
	next.Control = &c
	return t.persist(next)
}

// CheckCurrent is called on every signing attempt in addition to the committed
// publication fence supplied by C. Revocation takes effect independently of a
// material load, even when a revoked signer advertises an arbitrarily high keyset.
func (t *Trust) CheckCurrent(p api.Publication) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	c := t.state.Control
	if c == nil {
		return Fault("control_required")
	}
	if slices.Contains(c.Payload.RevokedInstallationIds, t.pin.IdentityId) {
		return Fault("authority_revoked")
	}
	if slices.Contains(c.Payload.RevokedAuthorityIds, p.Authority.Payload.AuthorityId) {
		return Fault("authority_revoked")
	}
	if !allowed(c.Payload, p.Authority) {
		return Fault("authority_unknown")
	}
	if p.Keyset.Payload.Generation != c.Payload.MinimumGeneration || lt(p.Keyset.Payload.KeysetVersion, c.Payload.MinimumKeysetVersion) {
		return Fault("version_rollback")
	}
	if !stamp(p.Keyset.Payload.ValidUntil).After(t.now()) {
		return Fault("invalid_transition")
	}
	return nil
}
func (t *Trust) Accept(p api.Publication, bundle api.SignedTransportTrustBundle) error {
	return t.accept(p, bundle, true)
}
func (t *Trust) VerifyCandidate(p api.Publication, bundle api.SignedTransportTrustBundle) error {
	return t.accept(p, bundle, false)
}
func (t *Trust) accept(p api.Publication, bundle api.SignedTransportTrustBundle, install bool) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	raw, e := Bytes(p)
	if e != nil {
		return e
	}
	if e = t.schemas.Decode("Publication", raw, &p); e != nil {
		return e
	}
	raw, e = Bytes(bundle)
	if e != nil {
		return e
	}
	if e = t.schemas.Decode("SignedTransportTrustBundle", raw, &bundle); e != nil {
		return e
	}
	if t.state.Control == nil || lt(t.state.Control.Payload.ControlRevision, p.Control.Payload.ControlRevision) {
		if e = t.updateControl(p.Control); e != nil {
			return e
		}
	}
	if slices.Contains(t.state.Control.Payload.RevokedInstallationIds, t.pin.IdentityId) {
		return Fault("authority_revoked")
	}
	f := Facts{RootSPKI: t.root, Tenant: t.tenant, Realm: t.realm, Now: t.now(), Latest: *t.state.Control, SeenControl: t.state.Control.Payload.ControlRevision}
	if old := t.state.Publication; old != nil {
		f.SeenGeneration = old.Keyset.Payload.Generation
		f.SeenVersion = old.Keyset.Payload.KeysetVersion
		f.SeenDigest, _ = ObjectDigest(old.Keyset)
		if q := p.Predecessor; q != nil && q.Keyset.Payload.Generation == f.SeenGeneration && q.Keyset.Payload.KeysetVersion == f.SeenVersion {
			f.PreviousDigest = f.SeenDigest
		}
	}
	for _, proof := range t.state.First {
		f.First = append(f.First, proof)
	}
	if e = VerifyPublication(p, f); e != nil {
		return e
	}
	if p.Keyset.Payload.Mode == "bootstrap" && t.state.Publication != nil && !Equal(*t.state.Publication, p) {
		return Fault("invalid_transition")
	}
	for _, key := range p.Keyset.Payload.Keys {
		if old, ok := t.state.Keys[key.Kid]; ok {
			if old.Purpose != key.Purpose || old.PublicKeySpki != key.PublicKeySpki || old.NotBefore != key.NotBefore || old.NotAfter != key.NotAfter || ((old.State == "retired" || old.State == "revoked") && old.State != key.State) {
				return Fault("invalid_transition")
			}
		}
		for id, old := range t.state.Keys {
			if id != key.Kid && old.PublicKeySpki == key.PublicKeySpki {
				return Fault("invalid_transition")
			}
		}
	}
	tf := TransportFacts{ControlRevision: t.state.Control.Payload.ControlRevision}
	if old := t.state.Bundle; old != nil {
		if Equal(*old, bundle) {
			tf.SeenVersion = ""
		} else {
			tf.SeenVersion = old.Payload.BundleVersion
			tf.PreviousPhase = old.Payload.Phase
			tf.Previous = old
		}
	}
	if t.confirmTransport != nil {
		if e = t.confirmTransport(p, bundle, *t.state.Control, t.state.Bundle, t.now()); e != nil {
			return e
		}
	} else {
		if t.state.Bundle != nil && (t.state.Bundle.Payload.Generation != bundle.Payload.Generation || t.state.Bundle.Payload.AuthorityDigest != bundle.Payload.AuthorityDigest) {
			return Fault("control_required")
		}
		if e = VerifyTransport(bundle, p, tf); e != nil {
			return e
		}
	}
	sameBundle := t.state.Bundle != nil && Equal(*t.state.Bundle, bundle)
	next := ledger{Control: t.state.Control, Publication: &p, Bundle: &bundle, Keys: map[string]api.PublicKey{}, First: map[string]api.FirstPublication{}, TransportCAs: map[string]transportCAHistory{}}
	for id, entry := range t.state.TransportCAs {
		next.TransportCAs[id] = entry
	}
	// Upgrade the K2 ledger without losing the last accepted CA identities.
	if t.state.Bundle != nil {
		for _, ca := range t.state.Bundle.Payload.Cas {
			if _, ok := next.TransportCAs[ca.CaId]; !ok {
				next.TransportCAs[ca.CaId] = transportCAHistory{CA: ca}
			}
		}
	}
	for _, ca := range bundle.Payload.Cas {
		if old, ok := next.TransportCAs[ca.CaId]; ok && (old.Retired || !Equal(old.CA, ca)) {
			return Fault("invalid_transition")
		}
		for id, old := range next.TransportCAs {
			if id != ca.CaId && old.CA.CertificateDer == ca.CertificateDer {
				return Fault("invalid_transition")
			}
		}
		next.TransportCAs[ca.CaId] = transportCAHistory{CA: ca}
	}
	for id, old := range next.TransportCAs {
		if !slices.ContainsFunc(bundle.Payload.Cas, func(ca api.TransportCa) bool { return ca.CaId == id }) {
			old.Retired = true
			next.TransportCAs[id] = old
		}
	}
	if sameBundle {
		next.ReaderPersisted = t.state.ReaderPersisted
		next.HandshakeVerified = t.state.HandshakeVerified
	}
	for k, v := range t.state.Keys {
		next.Keys[k] = v
	}
	for k, v := range t.state.First {
		next.First[k] = v
	}
	for _, key := range p.Keyset.Payload.Keys {
		next.Keys[key.Kid] = key
	}
	if p.Predecessor != nil {
		for _, proof := range p.Predecessor.Keyset.Payload.FirstPublications {
			next.First[proof.Kid] = proof
		}
	}
	for _, proof := range p.Keyset.Payload.FirstPublications {
		next.First[proof.Kid] = proof
	}
	if !install {
		return nil
	}
	return t.persist(next)
}

func (t *Trust) RecordLoaded(s api.LocalProjectionSelector, handshake bool) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.state.Publication == nil || t.state.Bundle == nil {
		return Fault("projection_pending")
	}
	pd, _ := ObjectDigest(t.state.Publication.Keyset)
	bd, _ := ObjectDigest(*t.state.Bundle)
	if pd != s.PublicationDigest || bd != s.BundleDigest {
		return Fault("projection_pending")
	}
	next := t.state
	next.ReaderPersisted = true
	if handshake {
		next.HandshakeVerified = true
	}
	return t.persist(next)
}
