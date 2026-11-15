package projection

import (
	"bytes"
	"crypto/ed25519"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"slices"
	"time"

	api "github.com/hansjone/auriga/infra/gen/msgnetapi"
)

func binaryText(s string) ([]byte, error) {
	b, e := base64.RawURLEncoding.Strict().DecodeString(s)
	if e != nil || base64.RawURLEncoding.EncodeToString(b) != s {
		return nil, Fault("invalid_chain")
	}
	return b, nil
}
func public(s string) (ed25519.PublicKey, error) {
	b, e := binaryText(s)
	if e != nil {
		return nil, e
	}
	v, e := x509.ParsePKIXPublicKey(b)
	if e != nil {
		return nil, Fault("invalid_chain")
	}
	k, ok := v.(ed25519.PublicKey)
	if !ok {
		return nil, Fault("invalid_chain")
	}
	der, e := x509.MarshalPKIXPublicKey(k)
	if e != nil || !bytes.Equal(der, b) {
		return nil, Fault("invalid_chain")
	}
	return k, nil
}
func VerifySignature(payload any, auth api.Signature, domain, spki string) error {
	p, e := public(spki)
	if e != nil {
		return e
	}
	sig, e := binaryText(auth.Signature)
	if e != nil {
		return e
	}
	b, e := Bytes(payload)
	if e != nil {
		return e
	}
	if auth.Alg != "Ed25519" || !ed25519.Verify(p, append([]byte(domain+"\x00"), b...), sig) {
		return Fault("invalid_chain")
	}
	return nil
}
func stamp(s string) time.Time       { t, _ := time.Parse("2006-01-02T15:04:05Z", s); return t }
func span(a, b string) time.Duration { return stamp(b).Sub(stamp(a)) }
func lt(a, b string) bool            { x, _ := number(a); y, _ := number(b); return x < y }
func allowed(c api.RootControl, a api.SignedRuntimeAuthority) bool {
	d, e := ObjectDigest(a)
	if e != nil {
		return false
	}
	return slices.ContainsFunc(c.AllowedAuthorities, func(x api.AuthorityReference) bool {
		return x.AuthorityId == a.Payload.AuthorityId && x.AuthorityDigest == d
	})
}

// Facts are supplied by the reader's durable ledger and local clock. They are
// never populated from unverified metadata in an incoming material directory.
type Facts struct {
	RootSPKI, Tenant, Realm                                              string
	Now                                                                  time.Time
	Latest                                                               api.SignedRootControl
	SeenControl, SeenGeneration, SeenVersion, SeenDigest, PreviousDigest string
	First                                                                []api.FirstPublication
}

func VerifyPublication(p api.Publication, f Facts) error {
	k, a, c := p.Keyset.Payload, p.Authority.Payload, f.Latest.Payload
	for _, s := range []struct {
		payload any
		auth    api.Signature
		domain  string
	}{{f.Latest.Payload, f.Latest.RootAuth, "Auriga.NetworkRootControl.v2"}, {p.Control.Payload, p.Control.RootAuth, "Auriga.NetworkRootControl.v2"}, {p.Authority.Payload, p.Authority.RootAuth, "Auriga.NetworkRuntimeAuthority.v2"}} {
		if e := VerifySignature(s.payload, s.auth, s.domain, f.RootSPKI); e != nil {
			return e
		}
	}
	if slices.Contains(c.RevokedAuthorityIds, a.AuthorityId) {
		return Fault("authority_revoked")
	}
	if !allowed(c, p.Authority) {
		return Fault("authority_unknown")
	}
	if k.ControlRevision != p.Control.Payload.ControlRevision {
		return Fault("invalid_chain")
	}
	if lt(c.ControlRevision, k.ControlRevision) {
		return Fault("control_required")
	}
	if lt(c.ControlRevision, f.SeenControl) {
		return Fault("control_rollback")
	}
	has := func(s string) bool { return slices.Contains(a.Capabilities, s) }
	if a.ExpiryPolicy != "no_hard_expiry" || !has("keyset") || (k.Mode == "restart_recovery" && !has("restart_recovery")) {
		return Fault("scope_denied")
	}
	ad, _ := ObjectDigest(p.Authority)
	if a.TenantId != k.TenantId || a.Realm != k.Realm || c.TenantId != k.TenantId || c.Realm != k.Realm || k.TenantId != f.Tenant || k.Realm != f.Realm || k.AuthorityId != a.AuthorityId || k.AuthorityDigest != ad || stamp(a.NotBefore).After(f.Now) {
		return Fault("scope_denied")
	}
	if k.Generation != c.MinimumGeneration || lt(k.KeysetVersion, c.MinimumKeysetVersion) || lt(k.Generation, f.SeenGeneration) || (k.Generation == f.SeenGeneration && lt(k.KeysetVersion, f.SeenVersion)) {
		return Fault("version_rollback")
	}
	kd, _ := ObjectDigest(p.Keyset)
	if k.Generation == f.SeenGeneration && k.KeysetVersion == f.SeenVersion && kd != f.SeenDigest {
		return Fault("same_version_conflict")
	}
	if p.Keyset.RuntimeAuth.Kid != a.RuntimeKid {
		return Fault("scope_denied")
	}
	if e := VerifySignature(k, p.Keyset.RuntimeAuth, "Auriga.NetworkKeyset.v2", a.PublicKeySpki); e != nil {
		return e
	}
	issued := stamp(k.IssuedAt)
	if issued.After(f.Now.Add(time.Minute)) || !stamp(k.ValidUntil).After(f.Now) || span(k.IssuedAt, k.ValidUntil) <= 0 || span(k.IssuedAt, k.ValidUntil) > time.Duration(a.Limits.KeysetSeconds)*time.Second {
		return Fault("invalid_transition")
	}
	if k.Mode != "bootstrap" && k.Mode != "migration" && p.Predecessor == nil {
		return Fault("predecessor_required")
	}
	var prev []api.PublicKey
	if q := p.Predecessor; q != nil {
		qk, qa, qc := q.Keyset.Payload, q.Authority.Payload, q.Control.Payload
		d, _ := ObjectDigest(q.Keyset)
		if d != q.KeysetDigest || (qk.Generation == k.Generation && !lt(qk.KeysetVersion, k.KeysetVersion)) {
			return Fault("invalid_transition")
		}
		if e := VerifySignature(qc, q.Control.RootAuth, "Auriga.NetworkRootControl.v2", f.RootSPKI); e != nil {
			return e
		}
		if e := VerifySignature(qa, q.Authority.RootAuth, "Auriga.NetworkRuntimeAuthority.v2", f.RootSPKI); e != nil {
			return e
		}
		qd, _ := ObjectDigest(q.Authority)
		if qk.AuthorityDigest != qd || qk.AuthorityId != qa.AuthorityId || qk.ControlRevision != qc.ControlRevision || qk.Realm != k.Realm || qk.TenantId != k.TenantId || qa.Realm != k.Realm || qa.TenantId != k.TenantId || qc.Realm != k.Realm || qc.TenantId != k.TenantId || !allowed(qc, q.Authority) || slices.Contains(qc.RevokedAuthorityIds, qa.AuthorityId) || q.Keyset.RuntimeAuth.Kid != qa.RuntimeKid {
			return Fault("invalid_chain")
		}
		if e := VerifySignature(qk, q.Keyset.RuntimeAuth, "Auriga.NetworkKeyset.v2", qa.PublicKeySpki); e != nil {
			return e
		}
		if !slices.Contains(qa.Capabilities, "keyset") || stamp(qa.NotBefore).After(stamp(qk.IssuedAt)) || span(qk.IssuedAt, qk.ValidUntil) <= 0 || span(qk.IssuedAt, qk.ValidUntil) > time.Duration(qa.Limits.KeysetSeconds)*time.Second || lt(qk.Generation, qc.MinimumGeneration) || lt(qk.KeysetVersion, qc.MinimumKeysetVersion) {
			return Fault("invalid_chain")
		}
		if qk.Generation != k.Generation || slices.Contains(c.RevokedAuthorityIds, qa.AuthorityId) {
			t := c.GenerationTransition
			if k.Mode != "authority_transition" || t == nil || t.PredecessorDigest != q.KeysetDigest || t.FromGeneration != qk.Generation || t.ToGeneration != k.Generation || t.AuthorityId != a.AuthorityId || t.AuthorityDigest != ad || t.FirstVersion != k.KeysetVersion || t.FirstVersion != c.MinimumKeysetVersion || !lt(qk.Generation, k.Generation) {
				return Fault("invalid_transition")
			}
			if slices.Contains(c.RevokedAuthorityIds, qa.AuthorityId) {
				for _, old := range qk.Keys {
					if old.State == "active" || old.State == "next" {
						if !slices.ContainsFunc(k.Keys, func(n api.PublicKey) bool { return n.Kid == old.Kid && n.State == "revoked" }) {
							return Fault("invalid_transition")
						}
					}
				}
				for _, n := range k.Keys {
					if n.State == "active" && slices.ContainsFunc(qk.Keys, func(o api.PublicKey) bool { return o.Kid == n.Kid }) {
						return Fault("invalid_transition")
					}
				}
			}
		} else if k.Mode == "authority_transition" && qa.AuthorityId == a.AuthorityId {
			return Fault("invalid_transition")
		}
		if f.PreviousDigest != "" && f.PreviousDigest != q.KeysetDigest {
			return Fault("invalid_transition")
		}
		prev = qk.Keys
	}
	find := func(keys []api.PublicKey, kid string) *api.PublicKey {
		for j := range keys {
			if keys[j].Kid == kid {
				return &keys[j]
			}
		}
		return nil
	}
	for _, n := range k.Keys {
		if find(prev, n.Kid) == nil && !has("business_rotation") {
			return Fault("scope_denied")
		}
	}
	for _, o := range prev {
		if find(k.Keys, o.Kid) == nil {
			if !has("history_archive") {
				return Fault("scope_denied")
			}
			if stamp(k.Archive.Before).After(issued.Add(-time.Duration(a.Limits.ArchiveGraceSeconds)*time.Second)) || stamp(o.NotAfter).After(stamp(k.Archive.Before)) {
				return Fault("invalid_transition")
			}
		}
	}
	snapshots := []api.KeysetPayload{k}
	if p.Predecessor != nil {
		snapshots = append([]api.KeysetPayload{p.Predecessor.Keyset.Payload}, snapshots...)
	}
	for _, snap := range snapshots {
		for _, proof := range snap.FirstPublications {
			for _, known := range f.First {
				if known.Kid == proof.Kid && !Equal(known, proof) {
					return Fault("invalid_transition")
				}
			}
			if lt(snap.KeysetVersion, proof.CommittedVersion) || stamp(proof.CommittedAt).After(stamp(snap.IssuedAt)) {
				return Fault("invalid_transition")
			}
		}
	}
	ids, materials := map[string]bool{}, map[string]bool{}
	for _, n := range k.Keys {
		o := find(prev, n.Kid)
		if o == nil && !slices.ContainsFunc(k.FirstPublications, func(proof api.FirstPublication) bool {
			return proof.Kid == n.Kid && proof.CommittedVersion == k.KeysetVersion && proof.CommittedAt == k.IssuedAt
		}) {
			return Fault("invalid_transition")
		}
		if ids[n.Kid] || materials[n.PublicKeySpki] || span(n.NotBefore, n.NotAfter) <= 0 || span(n.NotBefore, n.NotAfter) > time.Duration(a.Limits.BusinessKeySeconds)*time.Second {
			return Fault("invalid_transition")
		}
		ids[n.Kid] = true
		materials[n.PublicKeySpki] = true
		if o != nil && (o.PublicKeySpki != n.PublicKeySpki || o.Purpose != n.Purpose || o.NotBefore != n.NotBefore || o.NotAfter != n.NotAfter || ((o.State == "retired" || o.State == "revoked") && o.State != n.State)) {
			return Fault("invalid_transition")
		}
	}
	allValid := true
	valid := func(n *api.PublicKey) bool {
		return n != nil && !stamp(n.NotBefore).After(issued) && stamp(n.NotAfter).After(issued)
	}
	for _, purpose := range []string{"business_delivery", "protocol_response", "management_state"} {
		var n, o, mature *api.PublicKey
		count := 0
		for j := range k.Keys {
			if k.Keys[j].Purpose == purpose && k.Keys[j].State == "active" {
				n = &k.Keys[j]
				count++
			}
		}
		if count != 1 || !valid(n) {
			return Fault("invalid_transition")
		}
		for j := range prev {
			x := &prev[j]
			if x.Purpose != purpose {
				continue
			}
			if x.State == "active" {
				o = x
			}
			if x.State == "next" && valid(x) && p.Predecessor != nil {
				for _, proof := range p.Predecessor.Keyset.Payload.FirstPublications {
					if proof.Kid == x.Kid && !stamp(proof.CommittedAt).After(issued.Add(-time.Duration(a.Limits.NextMaturitySeconds)*time.Second)) && !lt(p.Predecessor.Keyset.Payload.KeysetVersion, proof.CommittedVersion) {
						mature = x
					}
				}
			}
		}
		if !valid(o) {
			allValid = false
		}
		same := o != nil && o.Kid == n.Kid
		if k.Mode == "refresh" && !same {
			return Fault("invalid_transition")
		}
		if k.Mode == "authority_transition" && p.Predecessor != nil && p.Predecessor.Keyset.Payload.Generation == k.Generation && !slices.Contains(c.RevokedAuthorityIds, p.Predecessor.Authority.Payload.AuthorityId) && !same {
			return Fault("invalid_transition")
		}
		if k.Mode == "activate" && !same && (mature == nil || mature.Kid != n.Kid || (valid(o) && stamp(o.NotAfter).Sub(issued) < time.Duration(a.Limits.OverlapSeconds)*time.Second)) {
			return Fault("invalid_transition")
		}
		if k.Mode == "restart_recovery" {
			// A next which has not completed its original maturity interval must
			// retain its identity and lifetime as a terminal entry, even when a
			// different purpose needs restart recovery.
			for _, x := range prev {
				if x.Purpose != purpose || x.State != "next" {
					continue
				}
				matureAt := false
				for _, proof := range p.Predecessor.Keyset.Payload.FirstPublications {
					if proof.Kid == x.Kid && !stamp(proof.CommittedAt).After(issued.Add(-time.Duration(a.Limits.NextMaturitySeconds)*time.Second)) {
						matureAt = true
					}
				}
				if !matureAt {
					next := find(k.Keys, x.Kid)
					if next == nil || (next.State != "retired" && next.State != "revoked") {
						return Fault("invalid_transition")
					}
				}
			}
			if valid(o) {
				if !same {
					return Fault("invalid_transition")
				}
			} else if mature != nil {
				if mature.Kid != n.Kid {
					return Fault("invalid_transition")
				}
			} else if find(prev, n.Kid) != nil || n.NotBefore != k.IssuedAt || stamp(n.NotAfter).Sub(issued) > time.Duration(a.Limits.RestartKeySeconds)*time.Second {
				return Fault("invalid_transition")
			}
		}
	}
	if k.Mode == "restart_recovery" && allValid {
		return Fault("invalid_transition")
	}
	return nil
}
func MatchPrivate(encoded string, key api.PublicKey) (ed25519.PrivateKey, error) {
	raw, e := binaryText(encoded)
	if e != nil {
		return nil, e
	}
	defer clear(raw)
	parsed, e := x509.ParsePKCS8PrivateKey(raw)
	if e != nil {
		return nil, Fault("invalid_chain")
	}
	k, ok := parsed.(ed25519.PrivateKey)
	if !ok {
		return nil, Fault("invalid_chain")
	}
	p, e := public(key.PublicKeySpki)
	if e != nil || !k.Public().(ed25519.PublicKey).Equal(p) {
		clear(k)
		return nil, errors.New("projection: private/public mismatch")
	}
	return k, nil
}
