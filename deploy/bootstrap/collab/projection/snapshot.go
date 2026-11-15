package projection

import (
	"crypto/ed25519"
	"crypto/x509"
	"encoding/base64"
	"fmt"
	"slices"
	"time"

	api "github.com/hansjone/auriga/infra/gen/msgnetapi"
)

type Snapshot struct {
	Selector    api.LocalProjectionSelector
	Publication api.Publication
	Protocol    *api.ProtocolMaterial
	Key         ed25519.PrivateKey
	TLS         *TLSSet
}

func (s *Snapshot) Clear() {
	if s != nil {
		clear(s.Key)
	}
}
func (t *Trust) Load(g *Generation) (*Snapshot, error) {
	return t.load(g, false)
}
func (t *Trust) load(g *Generation, historical bool) (*Snapshot, error) {
	if e := CheckFiles(g.Selector, g.Manifest, g.Files, g.Selector, nil); e != nil {
		return nil, e
	}
	s := g.Selector
	if s.TenantId != t.tenant || s.Realm != t.realm || s.InstallationId != t.pin.IdentityId || s.Component != t.pin.Component {
		return nil, Fault("scope_denied")
	}
	var p api.Publication
	var bundle api.SignedTransportTrustBundle
	if e := t.schemas.Decode("Publication", g.Files["publication.json"], &p); e != nil {
		return nil, e
	}
	if e := t.schemas.Decode("SignedTransportTrustBundle", g.Files["bundle.json"], &bundle); e != nil {
		return nil, e
	}
	pd, _ := ObjectDigest(p.Keyset)
	bd, _ := ObjectDigest(bundle)
	if pd != s.PublicationDigest || bd != s.BundleDigest || p.Keyset.Payload.Generation != s.Generation || p.Keyset.Payload.KeysetVersion != s.KeysetVersion || bundle.Payload.BundleVersion != s.BundleVersion {
		return nil, Fault("invalid_chain")
	}
	m := api.ProjectionMaterial{TenantId: s.TenantId, Realm: s.Realm, InstallationId: s.InstallationId, Component: s.Component, Nonce: s.Nonce, Generation: s.Generation, KeysetVersion: s.KeysetVersion, PublicationDigest: s.PublicationDigest, Bundle: bundle}
	snap := &Snapshot{Selector: s, Publication: p}
	success := false
	defer func() {
		if !success {
			snap.Clear()
		}
	}()
	if raw, ok := g.Files["protocol.json"]; ok {
		if s.Component != "network" {
			return nil, Fault("scope_denied")
		}
		var protocol api.ProtocolMaterial
		if e := t.schemas.Decode("ProtocolMaterial", raw, &protocol); e != nil {
			return nil, e
		}
		if !Equal(protocol.Publication, p) {
			return nil, Fault("invalid_chain")
		}
		active := slices.IndexFunc(p.Keyset.Payload.Keys, func(k api.PublicKey) bool {
			return k.Kid == protocol.ActiveKid && k.Purpose == "protocol_response" && k.State == "active"
		})
		if active < 0 {
			return nil, Fault("invalid_chain")
		}
		key, e := MatchPrivate(protocol.ActivePkcs8, p.Keyset.Payload.Keys[active])
		if e != nil {
			return nil, e
		}
		snap.Key = key
		if (protocol.NextKid == nil) != (protocol.NextPkcs8 == nil) {
			return nil, Fault("invalid_chain")
		}
		if protocol.NextKid != nil {
			next := slices.IndexFunc(p.Keyset.Payload.Keys, func(k api.PublicKey) bool {
				return k.Kid == *protocol.NextKid && k.Purpose == "protocol_response" && k.State == "next"
			})
			if next < 0 {
				return nil, Fault("invalid_chain")
			}
			key, e := MatchPrivate(*protocol.NextPkcs8, p.Keyset.Payload.Keys[next])
			if e != nil {
				return nil, e
			}
			clear(key)
		}
		snap.Protocol = &protocol
		m.Protocol = &protocol
	} else if s.Component == "network" {
		return nil, Fault("invalid_chain")
	}
	count := 0
	tlsTime := t.now()
	if historical {
		tlsTime = time.Time{}
	}
	for j := 0; j < 126; j++ {
		certRaw, exists := g.Files[fmt.Sprintf("leaf-%03d.cert.pem", j)]
		if !exists {
			break
		}
		count++
		der, e := DecodePEM(certRaw, "CERTIFICATE")
		if e != nil {
			return nil, e
		}
		private, e := DecodePEM(g.Files[fmt.Sprintf("leaf-%03d.key.pem", j)], "PRIVATE KEY")
		if e != nil {
			return nil, e
		}
		cert, e := x509.ParseCertificate(der)
		if e != nil {
			return nil, Fault("invalid_chain")
		}
		if historical && cert.NotBefore.After(tlsTime) {
			tlsTime = cert.NotBefore
		}
		role := ""
		if len(cert.ExtKeyUsage) != 1 {
			return nil, Fault("scope_denied")
		}
		switch s.Component {
		case "network":
			switch cert.ExtKeyUsage[0] {
			case x509.ExtKeyUsageServerAuth:
				role = "submit"
			case x509.ExtKeyUsageClientAuth:
				role = "broker-client"
			}
		case "broker":
			role = "broker"
		case "edge":
			switch cert.ExtKeyUsage[0] {
			case x509.ExtKeyUsageServerAuth:
				role = "edge"
			case x509.ExtKeyUsageClientAuth:
				role = "edge-presence"
			}
		}
		caID := ""
		for _, ca := range bundle.Payload.Cas {
			b, e := binaryText(ca.CertificateDer)
			if e != nil {
				return nil, e
			}
			c, e := x509.ParseCertificate(b)
			if e == nil && cert.CheckSignatureFrom(c) == nil {
				caID = ca.CaId
				break
			}
		}
		m.Leaves = append(m.Leaves, api.TlsLeaf{Component: s.Component, Role: role, CertificateDer: base64.RawURLEncoding.EncodeToString(der), PrivatePkcs8: base64.RawURLEncoding.EncodeToString(private), CaId: caID, NotBefore: cert.NotBefore.UTC().Format("2006-01-02T15:04:05Z"), NotAfter: cert.NotAfter.UTC().Format("2006-01-02T15:04:05Z")})
	}
	expected := 2 + 2*count
	if snap.Protocol != nil {
		expected++
	}
	if len(g.Files) != expected {
		return nil, Fault("invalid_chain")
	}
	if historical {
		tlsTime = tlsTime.Add(time.Second)
	}
	set, e := ValidateLeaves(m, t.pin, tlsTime)
	if e != nil {
		return nil, e
	}
	snap.TLS = set
	if historical {
		// Historical validation is used only to preserve a superseded transaction,
		// never to install expired/revoked material or generate a loaded receipt.
		if e = VerifyPublication(p, Facts{RootSPKI: t.root, Tenant: t.tenant, Realm: t.realm, Now: stamp(p.Keyset.Payload.IssuedAt), Latest: p.Control}); e != nil {
			return nil, e
		}
		if e = VerifyTransport(bundle, p, TransportFacts{ReadersPersisted: true, HandshakesVerified: true}); e != nil {
			return nil, e
		}
	} else if e = t.Accept(p, bundle); e != nil {
		return nil, e
	}
	success = true
	return snap, nil
}
