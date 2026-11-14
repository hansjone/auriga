package msgnet

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/x509"
	"encoding/base64"
	"fmt"
	"slices"
	"time"

	api "github.com/hansjone/auriga/infra/gen/msgnetapi"
)

func (i *Installer) InstallTransport(ctx context.Context, raw []byte) error {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.state == nil || i.options.ResolveAuthority == nil {
		return fmt.Errorf("control/authority required before transport")
	}
	if len(raw) > 32<<20 {
		return fmt.Errorf("transport bundle too large")
	}
	var bundle api.SignedTransportTrustBundle
	if e := strict(raw, &bundle); e != nil {
		return e
	}
	c := i.state.Package.Control.Payload
	p := bundle.Payload
	o := i.options
	// Root revocation always wins over a higher signed bundle version.
	if slices.Contains(c.RevokedAuthorityIds, p.AuthorityId) || slices.Contains(c.RevokedInstallationIds, o.Pin.IdentityId) {
		return fmt.Errorf("transport identity revoked")
	}
	allowed := false
	for _, reference := range c.AllowedAuthorities {
		if reference.AuthorityId == p.AuthorityId && reference.AuthorityDigest == p.AuthorityDigest {
			allowed = true
		}
	}
	if !allowed || p.TenantId != o.Tenant || p.Realm != o.Realm {
		return fmt.Errorf("transport scope denied")
	}
	authority, e := o.ResolveAuthority(ctx, p.AuthorityId, p.AuthorityDigest)
	if e != nil {
		return e
	}
	if e = verify(o.RootKey, o.RootKid, "Auriga.NetworkRuntimeAuthority.v2", authority.Payload, authority.RootAuth); e != nil {
		return e
	}
	encoded, e := Bytes(authority)
	if e != nil || Digest(encoded) != p.AuthorityDigest {
		return fmt.Errorf("authority digest mismatch")
	}
	a := authority.Payload
	if a.TenantId != o.Tenant || a.Realm != o.Realm || a.AuthorityId != p.AuthorityId || !slices.Contains(a.Capabilities, "network_transport") {
		return fmt.Errorf("transport capability denied")
	}
	notBefore, e := time.Parse(time.RFC3339, a.NotBefore)
	if e != nil || o.Now().Before(notBefore) {
		return fmt.Errorf("authority not active")
	}
	keyDER, e := base64.RawURLEncoding.Strict().DecodeString(a.PublicKeySpki)
	if e != nil {
		return e
	}
	key, e := x509.ParsePKIXPublicKey(keyDER)
	if e != nil {
		return e
	}
	runtimeKey, ok := key.(ed25519.PublicKey)
	if !ok || bytes.Equal(runtimeKey, o.RootKey) {
		return fmt.Errorf("invalid runtime signing key")
	}
	canonical, _ := x509.MarshalPKIXPublicKey(runtimeKey)
	if !bytes.Equal(keyDER, canonical) {
		return fmt.Errorf("noncanonical runtime key")
	}
	if e = verify(runtimeKey, a.RuntimeKid, "Auriga.NetworkTransportTrustBundle.v2", p, bundle.RuntimeAuth); e != nil {
		return e
	}
	revision, e := number(p.ControlRevision)
	if e != nil {
		return e
	}
	highest, _ := number(c.ControlRevision)
	generation, e := number(p.Generation)
	if e != nil {
		return e
	}
	minimum, _ := number(c.MinimumGeneration)
	version, e := number(p.BundleVersion)
	if e != nil {
		return e
	}
	issued, e := time.Parse(time.RFC3339, p.IssuedAt)
	if e != nil || issued.UTC().Format(time.RFC3339) != p.IssuedAt || issued.Before(notBefore) || issued.After(o.Now().Add(time.Minute)) {
		return fmt.Errorf("future transport bundle")
	}
	if fmt.Sprint(p.SchemaVersion) != "2" || fmt.Sprint(a.SchemaVersion) != "2" || a.ExpiryPolicy != "no_hard_expiry" || revision > highest || generation < minimum || len(p.Scope) == 0 || len(p.Cas) > 32 {
		return fmt.Errorf("transport control fence")
	}
	// This is a public relay, not a request for private projection material.
	// Its scopes name the TLS readers and need not contain this node identity.
	for _, scope := range p.Scope {
		permitted := false
		for _, grant := range a.TransportScope {
			if grant.Component == scope.Component && grant.IdentityId == scope.IdentityId && subset(scope.Sans, grant.Sans) && subset(scope.Ekus, grant.Ekus) {
				permitted = true
			}
		}
		if !permitted {
			return fmt.Errorf("transport scope exceeds authority")
		}
	}
	switch p.Phase {
	case "dual_trust", "new_leaves", "retire_old":
	default:
		return fmt.Errorf("unknown transport phase")
	}
	seen := map[string]bool{}
	for _, ca := range p.Cas {
		if seen[ca.CaId] || ca.CaId == "" || (ca.Role != "client" && ca.Role != "server") {
			return fmt.Errorf("invalid CA identity")
		}
		seen[ca.CaId] = true
		der, e := base64.RawURLEncoding.Strict().DecodeString(ca.CertificateDer)
		if e != nil {
			return e
		}
		cert, e := x509.ParseCertificate(der)
		if e != nil || !cert.IsCA || cert.CheckSignatureFrom(cert) != nil {
			return fmt.Errorf("invalid CA")
		}
		if ca.NotBefore != cert.NotBefore.UTC().Format(time.RFC3339) || ca.NotAfter != cert.NotAfter.UTC().Format(time.RFC3339) || bytes.Equal(cert.RawSubjectPublicKeyInfo, keyDER) {
			return fmt.Errorf("CA binding mismatch")
		}
	}
	if len(p.Cas) == 0 {
		return fmt.Errorf("missing transport CA")
	}
	if old := i.state.Bundle; old != nil {
		oldVersion, _ := number(old.Payload.BundleVersion)
		if version < oldVersion {
			return fmt.Errorf("transport rollback")
		}
		if version == oldVersion && !Equal(bundle, *old) {
			return fmt.Errorf("same transport version changed")
		}
		if version > oldVersion {
			// K2 defines the next CA cycle under the same runtime authority only.
			// A new authority must not silently reset transport history.
			if p.AuthorityId != old.Payload.AuthorityId || p.AuthorityDigest != old.Payload.AuthorityDigest || p.Generation != old.Payload.Generation || !Equal(p.Scope, old.Payload.Scope) {
				return fmt.Errorf("transport authority transition requires a frozen rule")
			}
			encoded, _ := Bytes(*old)
			if p.PreviousBundleDigest != Digest(encoded) {
				return fmt.Errorf("transport predecessor mismatch")
			}
			rank := map[string]int{"dual_trust": 0, "new_leaves": 1, "retire_old": 2}
			before, after := rank[old.Payload.Phase], rank[p.Phase]
			if after < before && !(before == 2 && after == 0) || after > before+1 {
				return fmt.Errorf("invalid transport phase transition")
			}
			if before == after && !Equal(p.Cas, old.Payload.Cas) {
				return fmt.Errorf("CA changed within a phase")
			}
			if before == 0 && after == 1 && !Equal(p.Cas, old.Payload.Cas) {
				return fmt.Errorf("new leaves dropped dual trust")
			}
			if before == 1 && after == 2 {
				for _, current := range p.Cas {
					found := false
					for _, previous := range old.Payload.Cas {
						if Equal(previous, current) {
							found = true
						}
					}
					if !found {
						return fmt.Errorf("retirement introduced a CA")
					}
				}
			}
		}
		if version > oldVersion && old.Payload.Phase == "retire_old" && p.Phase == "dual_trust" {
			encoded, _ := Bytes(*old)
			if p.PreviousBundleDigest != Digest(encoded) || len(p.Cas) <= len(old.Payload.Cas) {
				return fmt.Errorf("invalid next CA cycle")
			}
			for _, previous := range old.Payload.Cas {
				found := false
				for _, current := range p.Cas {
					if Equal(previous, current) {
						found = true
					}
				}
				if !found {
					return fmt.Errorf("previous CA missing")
				}
			}
		}
	}
	next := *i.state
	next.Bundle = &bundle
	if e = i.persist(&next); e != nil {
		return e
	}
	i.state = &next
	// Fence first, then apply. A crash or failed consumer must retry the exact
	// signed bundle instead of making an older bundle acceptable after restart.
	return o.Backend.ApplyTransport(ctx, bundle)
}
func subset(values, allowed []string) bool {
	for _, value := range values {
		if !slices.Contains(allowed, value) {
			return false
		}
	}
	return true
}
