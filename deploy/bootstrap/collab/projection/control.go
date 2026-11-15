package projection

import (
	"crypto/ed25519"
	"os"
	"path/filepath"
	"time"

	api "github.com/hansjone/auriga/infra/gen/msgnetapi"
)

// InstallControlPackage is the independent public-control import seam for D.
// Its Trust must use a dedicated import ledger, not the running reader's ledger.
// snapshotPath is trusted installation configuration, never a socket parameter.
func (t *Trust) InstallControlPackage(q api.ControlPackage, key ed25519.PrivateKey, snapshotPath, receiptsDir string) (api.InstallationReceipt, error) {
	var zero api.InstallationReceipt
	if len(key) != ed25519.PrivateKeySize {
		return zero, Fault("scope_denied")
	}
	pub, e := public(t.pin.SigningSpki)
	if e != nil || !key.Public().(ed25519.PublicKey).Equal(pub) {
		return zero, Fault("scope_denied")
	}
	raw, e := Bytes(q)
	if e != nil {
		return zero, e
	}
	if e = t.schemas.Decode("ControlPackage", raw, &q); e != nil {
		return zero, e
	}
	challenge, e := binaryText(q.Challenge)
	if e != nil || len(challenge) < 32 {
		return zero, Fault("invalid_transition")
	}
	digest, e := ObjectDigest(q.Control)
	if e != nil {
		return zero, e
	}
	if q.TenantId != t.tenant || q.Realm != t.realm || q.TargetId != t.pin.TargetId || q.ControlDigest != digest {
		return zero, Fault("scope_denied")
	}
	if e = privateDir(receiptsDir); e != nil {
		return zero, e
	}
	id := Digest(raw)
	path := filepath.Join(receiptsDir, id[7:]+".json")
	existing, e := readPrivateFile(path, 16384)
	if e == nil {
		var receipt api.InstallationReceipt
		if e = Decode(existing, &receipt, true); e != nil {
			return zero, e
		}
		if e = VerifyIdentity(receipt, receipt.IdentityAuth, "Auriga.NetworkControlReceipt.v2", t.pin.SigningSpki); e != nil {
			return zero, e
		}
		if receipt.PackageId != q.PackageId || receipt.TenantId != q.TenantId || receipt.Realm != q.Realm || receipt.TargetId != q.TargetId || receipt.IdentityId != t.pin.IdentityId || receipt.Challenge != q.Challenge || receipt.ControlDigest != q.ControlDigest || receipt.ControlRevision != q.Control.Payload.ControlRevision || receipt.Result != "installed" {
			return zero, Fault("invalid_transition")
		}
		if e = t.PublishControlSnapshot(q.Control, snapshotPath); e != nil {
			return zero, e
		}
		return receipt, nil
	} else if !os.IsNotExist(e) {
		return zero, e
	}
	if e = t.PublishControlSnapshot(q.Control, snapshotPath); e != nil {
		return zero, e
	}
	receipt := api.InstallationReceipt{PackageId: q.PackageId, TenantId: q.TenantId, Realm: q.Realm, TargetId: q.TargetId, IdentityId: t.pin.IdentityId, Challenge: q.Challenge, ControlRevision: q.Control.Payload.ControlRevision, ControlDigest: q.ControlDigest, Result: "installed", PersistedAt: time.Now().UTC().Truncate(time.Second).Format(time.RFC3339)}
	receipt.IdentityAuth, e = signIdentity(receipt, key, t.pin.IdentityId, "Auriga.NetworkControlReceipt.v2")
	if e != nil {
		return zero, e
	}
	raw, e = Bytes(receipt)
	if e != nil {
		return zero, e
	}
	if e = atomicFile(path, raw); e != nil {
		return zero, e
	}
	return receipt, nil
}
func ReadControlSnapshot(path string) ([]byte, error) { return readPrivateFile(path, MaxFile) }
func (t *Trust) PublishControlSnapshot(c api.SignedRootControl, path string) error {
	return t.publishControlSnapshot(c, path, true)
}
func (t *Trust) PublishObservedControl(c api.SignedRootControl, path string) error {
	return t.publishControlSnapshot(c, path, false)
}
func (t *Trust) publishControlSnapshot(c api.SignedRootControl, path string, strict bool) error {
	release, e := lockFile(filepath.Join(filepath.Dir(path), "root-control.lock"))
	if e != nil {
		return e
	}
	defer release()
	existing, e := ReadControlSnapshot(path)
	if e == nil {
		if e = t.ObserveControl(existing); e != nil {
			return e
		}
	} else if !os.IsNotExist(e) {
		return e
	}
	if strict {
		if e = t.UpdateControl(c); e != nil {
			return e
		}
	} else {
		raw, e := Bytes(c)
		if e != nil {
			return e
		}
		if e = t.ObserveControl(raw); e != nil {
			return e
		}
		t.mu.Lock()
		latest := t.state.Control
		if latest != nil {
			c = *latest
		}
		t.mu.Unlock()
		if latest == nil {
			return Fault("control_required")
		}
	}
	raw, e := Bytes(c)
	if e != nil {
		return e
	}
	return atomicFile(path, raw)
}
func (t *Trust) CheckLoadedCurrent() error {
	t.mu.Lock()
	p := t.state.Publication
	t.mu.Unlock()
	if p == nil {
		return Fault("projection_pending")
	}
	return t.CheckCurrent(*p)
}
func (t *Trust) ObserveControl(raw []byte) error {
	var c api.SignedRootControl
	if e := t.schemas.Decode("SignedRootControl", raw, &c); e != nil {
		return e
	}
	if e := VerifySignature(c.Payload, c.RootAuth, "Auriga.NetworkRootControl.v2", t.root); e != nil {
		return e
	}
	if c.Payload.TenantId != t.tenant || c.Payload.Realm != t.realm {
		return Fault("scope_denied")
	}
	t.mu.Lock()
	old := t.state.Control
	older := old != nil && lt(c.Payload.ControlRevision, old.Payload.ControlRevision)
	t.mu.Unlock()
	if older {
		return nil
	}
	return t.UpdateControl(c)
}
