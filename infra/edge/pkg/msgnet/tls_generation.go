package msgnet

import (
	"crypto/x509"
	"encoding/base64"
	"fmt"
	"slices"
	"time"

	api "github.com/hansjone/auriga/infra/gen/msgnetapi"
)

// DecodeTLSGeneration checks the exact immutable file set and private/public
// pairs. roleSAN is installation configuration, never an untrusted leaf claim.
// Trust signatures and durable control/version fences are separate required gates.
func DecodeTLSGeneration(g *Generation, schemas *Schemas, pin api.InstallationPin, roleSAN map[string]string, now time.Time) (api.Publication, api.SignedTransportTrustBundle, *TLSSet, error) {
	var p api.Publication
	var b api.SignedTransportTrustBundle
	fail := func(e error) (api.Publication, api.SignedTransportTrustBundle, *TLSSet, error) { return p, b, nil, e }
	if g == nil || schemas == nil || CheckFiles(g.Selector, g.Manifest, g.Files, g.Selector, nil) != nil || g.Selector.Component != pin.Component || g.Selector.InstallationId != pin.IdentityId {
		return fail(Fault("scope_denied"))
	}
	if e := schemas.Decode("Publication", g.Files["publication.json"], &p); e != nil {
		return fail(e)
	}
	if e := schemas.Decode("SignedTransportTrustBundle", g.Files["bundle.json"], &b); e != nil {
		return fail(e)
	}
	pd, _ := ObjectDigest(p.Keyset)
	bd, _ := ObjectDigest(b)
	s := g.Selector
	if pd != s.PublicationDigest || bd != s.BundleDigest || p.Keyset.Payload.Generation != s.Generation || p.Keyset.Payload.KeysetVersion != s.KeysetVersion || b.Payload.BundleVersion != s.BundleVersion || p.Keyset.Payload.TenantId != s.TenantId || p.Keyset.Payload.Realm != s.Realm {
		return fail(Fault("invalid_chain"))
	}
	m := api.ProjectionMaterial{TenantId: s.TenantId, Realm: s.Realm, InstallationId: s.InstallationId, Component: s.Component, Nonce: s.Nonce, Generation: s.Generation, KeysetVersion: s.KeysetVersion, PublicationDigest: s.PublicationDigest, Bundle: b}
	for j := 0; j < 126; j++ {
		raw, ok := g.Files[fmt.Sprintf("leaf-%03d.cert.pem", j)]
		if !ok {
			break
		}
		der, e := DecodePEM(raw, "CERTIFICATE")
		if e != nil {
			return fail(e)
		}
		key, e := DecodePEM(g.Files[fmt.Sprintf("leaf-%03d.key.pem", j)], "PRIVATE KEY")
		if e != nil {
			return fail(e)
		}
		cert, e := x509.ParseCertificate(der)
		if e != nil {
			return fail(Fault("invalid_chain"))
		}
		role := ""
		for name, san := range roleSAN {
			if san != "" && slices.Contains(cert.DNSNames, san) {
				if role != "" {
					return fail(Fault("scope_denied"))
				}
				role = name
			}
		}
		if role == "" || len(cert.ExtKeyUsage) != 1 || cert.ExtKeyUsage[0] != x509.ExtKeyUsageClientAuth {
			return fail(Fault("scope_denied"))
		}
		caID := ""
		for _, ca := range b.Payload.Cas {
			encoded, e := binaryText(ca.CertificateDer)
			if e != nil {
				return fail(e)
			}
			issuer, e := x509.ParseCertificate(encoded)
			if e == nil && cert.CheckSignatureFrom(issuer) == nil {
				if caID != "" {
					return fail(Fault("invalid_chain"))
				}
				caID = ca.CaId
			}
		}
		m.Leaves = append(m.Leaves, api.TlsLeaf{Component: s.Component, Role: role, CertificateDer: base64.RawURLEncoding.EncodeToString(der), PrivatePkcs8: base64.RawURLEncoding.EncodeToString(key), CaId: caID, NotBefore: cert.NotBefore.UTC().Format(time.RFC3339), NotAfter: cert.NotAfter.UTC().Format(time.RFC3339)})
	}
	if len(g.Files) != 2+2*len(m.Leaves) || len(m.Leaves) != len(roleSAN) {
		return fail(Fault("invalid_chain"))
	}
	set, e := ValidateLeaves(m, pin, now)
	return p, b, set, e
}
