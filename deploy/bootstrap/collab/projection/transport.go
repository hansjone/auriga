package projection

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"slices"
	"sort"
	"time"

	api "github.com/hansjone/auriga/infra/gen/msgnetapi"
)

type TransportFacts struct {
	SeenVersion, PreviousPhase           string
	ControlRevision                      string
	Previous                             *api.SignedTransportTrustBundle
	ReadersPersisted, HandshakesVerified bool
}

func VerifyTransport(t api.SignedTransportTrustBundle, p api.Publication, f TransportFacts) error {
	a := p.Authority.Payload
	k := p.Keyset.Payload
	if !slices.Contains(a.Capabilities, "network_transport") {
		return Fault("scope_denied")
	}
	if e := VerifySignature(t.Payload, t.RuntimeAuth, "Auriga.NetworkTransportTrustBundle.v2", a.PublicKeySpki); e != nil {
		return e
	}
	ad, _ := ObjectDigest(p.Authority)
	revision := f.ControlRevision
	if revision == "" {
		revision = p.Control.Payload.ControlRevision
	}
	if t.RuntimeAuth.Kid != a.RuntimeKid || t.Payload.TenantId != k.TenantId || t.Payload.Realm != k.Realm || t.Payload.AuthorityId != a.AuthorityId || t.Payload.AuthorityDigest != ad || t.Payload.Generation != k.Generation || lt(revision, t.Payload.ControlRevision) {
		return Fault("scope_denied")
	}
	for _, s := range t.Payload.Scope {
		if !slices.ContainsFunc(a.TransportScope, func(x api.TransportScope) bool { return Equal(x, s) }) {
			return Fault("scope_denied")
		}
	}
	if !lt(f.SeenVersion, t.Payload.BundleVersion) {
		return Fault("version_rollback")
	}
	cycle := f.PreviousPhase == "retire_old" && t.Payload.Phase == "dual_trust"
	if cycle {
		q := f.Previous
		if q == nil {
			return Fault("invalid_transition")
		}
		if e := VerifySignature(q.Payload, q.RuntimeAuth, "Auriga.NetworkTransportTrustBundle.v2", a.PublicKeySpki); e != nil {
			return e
		}
		d, _ := ObjectDigest(q)
		if q.Payload.TenantId != t.Payload.TenantId || q.Payload.Realm != t.Payload.Realm || q.Payload.AuthorityId != t.Payload.AuthorityId || q.Payload.AuthorityDigest != t.Payload.AuthorityDigest || q.Payload.Generation != t.Payload.Generation || q.Payload.Phase != "retire_old" || q.Payload.BundleVersion != f.SeenVersion || d != t.Payload.PreviousBundleDigest {
			return Fault("invalid_transition")
		}
		for _, o := range q.Payload.Cas {
			if !slices.ContainsFunc(t.Payload.Cas, func(n api.TransportCa) bool { return Equal(o, n) }) {
				return Fault("invalid_transition")
			}
		}
		if !slices.ContainsFunc(t.Payload.Cas, func(n api.TransportCa) bool {
			return !slices.ContainsFunc(q.Payload.Cas, func(o api.TransportCa) bool { return o.CaId == n.CaId })
		}) {
			return Fault("invalid_transition")
		}
	}
	phase := map[string]int{"dual_trust": 0, "new_leaves": 1, "retire_old": 2}
	if f.PreviousPhase != "" && !cycle && phase[t.Payload.Phase] != phase[f.PreviousPhase]+1 {
		return Fault("invalid_transition")
	}
	if t.Payload.Phase == "new_leaves" && !f.ReadersPersisted {
		return Fault("projection_pending")
	}
	if t.Payload.Phase == "retire_old" && !f.HandshakesVerified {
		return Fault("projection_pending")
	}
	return nil
}

// BuildGeneration constructs only the fixed local file set. The caller must
// verify the signed publication/bundle against durable trust before publishing.
func BuildGeneration(material api.ProjectionMaterial, publication api.Publication, revision string) (*Generation, error) {
	s := api.LocalProjectionSelector{TenantId: material.TenantId, Realm: material.Realm, InstallationId: material.InstallationId, Component: material.Component, Nonce: material.Nonce, Generation: material.Generation, KeysetVersion: material.KeysetVersion, PublicationDigest: material.PublicationDigest, BundleVersion: material.Bundle.Payload.BundleVersion, MaterialRevision: revision}
	s.BundleDigest, _ = ObjectDigest(material.Bundle)
	g := &Generation{Files: map[string][]byte{}}
	g.Selector = s
	put := func(name string, v any) error {
		b, e := Bytes(v)
		if e == nil {
			g.Files[name] = b
		}
		return e
	}
	if e := put("publication.json", publication); e != nil {
		return nil, e
	}
	if e := put("bundle.json", material.Bundle); e != nil {
		return nil, e
	}
	if material.Protocol != nil {
		if material.Component != "network" {
			return nil, Fault("scope_denied")
		}
		if e := put("protocol.json", material.Protocol); e != nil {
			return nil, e
		}
	}
	if len(material.Leaves) > 126 {
		return nil, Fault("invalid_chain")
	}
	for j, leaf := range material.Leaves {
		if leaf.Component != material.Component {
			return nil, Fault("scope_denied")
		}
		cert, e := binaryText(leaf.CertificateDer)
		if e != nil {
			return nil, e
		}
		key, e := binaryText(leaf.PrivatePkcs8)
		if e != nil {
			return nil, e
		}
		g.Files[fmt.Sprintf("leaf-%03d.cert.pem", j)] = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert})
		g.Files[fmt.Sprintf("leaf-%03d.key.pem", j)] = pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: key})
		clear(key)
	}
	m := api.LocalProjectionManifest{TenantId: s.TenantId, Realm: s.Realm, InstallationId: s.InstallationId, Component: s.Component, Nonce: s.Nonce, Generation: s.Generation, KeysetVersion: s.KeysetVersion, PublicationDigest: s.PublicationDigest, BundleVersion: s.BundleVersion, BundleDigest: s.BundleDigest, MaterialRevision: s.MaterialRevision}
	names := make([]string, 0, len(g.Files))
	for name := range g.Files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		m.Files = append(m.Files, api.LocalProjectionFile{Name: name, Size: int32(len(g.Files[name])), Sha256: Digest(g.Files[name])})
	}
	g.Manifest = m
	g.Selector.ManifestDigest, _ = ObjectDigest(m)
	return g, nil
}

// TLSSet is immutable after validation and can be swapped atomically as one
// unit. No CA private keys or another component's leaves are accepted.
type TLSSet struct {
	Leaves                   map[string]tls.Certificate
	ServerRoots, ClientRoots *x509.CertPool
}

func ValidateLeaves(m api.ProjectionMaterial, pin api.InstallationPin, now time.Time) (*TLSSet, error) {
	if pin.Component != m.Component || pin.IdentityId != m.InstallationId {
		return nil, Fault("scope_denied")
	}
	set := &TLSSet{Leaves: map[string]tls.Certificate{}, ServerRoots: x509.NewCertPool(), ClientRoots: x509.NewCertPool()}
	cas := map[string]*x509.Certificate{}
	roles := map[string]string{}
	for _, c := range m.Bundle.Payload.Cas {
		der, e := binaryText(c.CertificateDer)
		if e != nil {
			return nil, e
		}
		cert, e := x509.ParseCertificate(der)
		if e != nil || !cert.IsCA || !cert.BasicConstraintsValid || cert.KeyUsage&x509.KeyUsageCertSign == 0 || !cert.NotAfter.After(cert.NotBefore) || cert.NotAfter.After(cert.NotBefore.AddDate(10, 0, 0)) || cert.CheckSignatureFrom(cert) != nil || cert.NotBefore.UTC().Format(time.RFC3339) != c.NotBefore || cert.NotAfter.UTC().Format(time.RFC3339) != c.NotAfter {
			return nil, Fault("invalid_chain")
		}
		if cas[c.CaId] != nil {
			return nil, Fault("invalid_chain")
		}
		cas[c.CaId] = cert
		roles[c.CaId] = c.Role
		if c.Role == "server" && !cert.NotBefore.After(now) && cert.NotAfter.After(now) {
			set.ServerRoots.AddCert(cert)
		} else if c.Role == "client" && !cert.NotBefore.After(now) && cert.NotAfter.After(now) {
			set.ClientRoots.AddCert(cert)
		} else if c.Role != "server" && c.Role != "client" {
			return nil, Fault("invalid_chain")
		}
	}
	for _, leaf := range m.Leaves {
		if leaf.Component != m.Component || set.Leaves[leaf.Role].Certificate != nil {
			return nil, Fault("scope_denied")
		}
		var scope *api.TransportScope
		for j := range m.Bundle.Payload.Scope {
			s := &m.Bundle.Payload.Scope[j]
			if s.Component == m.Component && s.IdentityId == pin.IdentityId {
				scope = s
			}
		}
		if scope == nil {
			return nil, Fault("scope_denied")
		}
		der, e := binaryText(leaf.CertificateDer)
		if e != nil {
			return nil, e
		}
		cert, e := x509.ParseCertificate(der)
		if e != nil {
			return nil, Fault("invalid_chain")
		}
		key, e := binaryText(leaf.PrivatePkcs8)
		if e != nil {
			return nil, e
		}
		pair, e := tls.X509KeyPair(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: key}))
		clear(key)
		if e != nil {
			return nil, Fault("invalid_chain")
		}
		ca := cas[leaf.CaId]
		if ca == nil || cert.IsCA || cert.CheckSignatureFrom(ca) != nil || cert.NotBefore.After(now) || !cert.NotAfter.After(now) || cert.NotAfter.Sub(cert.NotBefore) > 90*24*time.Hour || ca.NotBefore.After(now) || !ca.NotAfter.After(now) || cert.NotAfter.After(ca.NotAfter) || cert.NotBefore.UTC().Format(time.RFC3339) != leaf.NotBefore || cert.NotAfter.UTC().Format(time.RFC3339) != leaf.NotAfter {
			return nil, Fault("invalid_chain")
		}
		if len(cert.URIs) != 0 || len(cert.EmailAddresses) != 0 || len(cert.UnknownExtKeyUsage) != 0 {
			return nil, Fault("scope_denied")
		}
		for _, name := range cert.DNSNames {
			if !slices.Contains(scope.Sans, name) {
				return nil, Fault("scope_denied")
			}
		}
		for _, ip := range cert.IPAddresses {
			if !slices.Contains(scope.Sans, ip.String()) {
				return nil, Fault("scope_denied")
			}
		}
		if len(cert.DNSNames)+len(cert.IPAddresses) == 0 || len(cert.ExtKeyUsage) == 0 {
			return nil, Fault("scope_denied")
		}
		for _, eku := range cert.ExtKeyUsage {
			role := ""
			switch eku {
			case x509.ExtKeyUsageServerAuth:
				role = "serverAuth"
			case x509.ExtKeyUsageClientAuth:
				role = "clientAuth"
			}
			if role == "" || !slices.Contains(scope.Ekus, role) {
				return nil, Fault("scope_denied")
			}
			if (role == "serverAuth" && roles[leaf.CaId] != "server") || (role == "clientAuth" && roles[leaf.CaId] != "client") {
				return nil, Fault("scope_denied")
			}
		}
		allowedRole := map[string][]string{"network": {"submit", "broker-client"}, "broker": {"broker"}, "edge": {"edge", "edge-presence"}}
		if !slices.Contains(allowedRole[m.Component], leaf.Role) {
			return nil, Fault("scope_denied")
		}
		pair.Leaf = cert
		set.Leaves[leaf.Role] = pair
	}
	return set, nil
}
func DecodePEM(raw []byte, kind string) ([]byte, error) {
	b, rest := pem.Decode(raw)
	if b == nil || b.Type != kind || len(rest) != 0 || !bytes.Equal(raw, pem.EncodeToMemory(b)) {
		return nil, Fault("invalid_chain")
	}
	return b.Bytes, nil
}
