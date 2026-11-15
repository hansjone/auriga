package app

import (
	"crypto/ed25519"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/hansjone/auriga/infra/agent/internal/identity"
	"github.com/hansjone/auriga/infra/agent/internal/msgnet"
	trust "github.com/hansjone/auriga/infra/edge/pkg/msgnet"
	api "github.com/hansjone/auriga/infra/gen/msgnetapi"
)

type msgnetRunConfig struct {
	RootSPKI     string `json:"rootSpki"`
	RootKid      string `json:"rootKid"`
	NodePinsFile string `json:"nodePinsFile"`
	SchemaFile   string `json:"schemaFile"`
	ProofOrigin  string `json:"proofOrigin"`
	ProofCAFile  string `json:"proofCaFile"`
}

func newMsgnetInstaller(agentDir, file string) (*msgnet.Installer, func(), error) {
	noop := func() {}
	raw, e := identity.ReadPrivateConfiguration(file, 65536)
	if e != nil {
		return nil, noop, e
	}
	var cfg msgnetRunConfig
	if e = trust.Decode(raw, &cfg, false); e != nil {
		return nil, noop, e
	}
	der, e := base64.RawURLEncoding.Strict().DecodeString(cfg.RootSPKI)
	if e != nil {
		return nil, noop, e
	}
	root, e := x509.ParsePKIXPublicKey(der)
	if e != nil {
		return nil, noop, e
	}
	rootKey, ok := root.(ed25519.PublicKey)
	if !ok {
		return nil, noop, fmt.Errorf("independent Ed25519 root required")
	}
	schemas, e := trust.LoadSchemas(cfg.SchemaFile)
	if e != nil {
		return nil, noop, e
	}
	raw, e = identity.ReadPrivateConfiguration(cfg.NodePinsFile, 32<<20)
	if e != nil {
		return nil, noop, e
	}
	var pins api.NodeControlPins
	if e = schemas.Decode("NodeControlPins", raw, &pins); e != nil {
		return nil, noop, e
	}
	material, e := identity.Load(agentDir)
	if e != nil {
		return nil, noop, e
	}
	var selected *api.NodeControlPin
	nodes, ids, targets, keys := map[string]bool{}, map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, p := range pins.Pins {
		if nodes[p.NodeId] || ids[p.IdentityId] || targets[p.TargetId] || keys[p.SigningSpki] {
			return nil, noop, fmt.Errorf("duplicate node control pin")
		}
		nodes[p.NodeId], ids[p.IdentityId], targets[p.TargetId], keys[p.SigningSpki] = true, true, true, true
		if p.NodeId == material.NodeID {
			copy := p
			selected = &copy
		}
	}
	if selected == nil || !regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:@-]{0,127}$`).MatchString(selected.TargetId) {
		return nil, noop, fmt.Errorf("native node is absent from protected confirmation pins")
	}
	directory, e := identity.MsgnetIdentityDir(agentDir, selected.IdentityId)
	if e != nil {
		return nil, noop, e
	}
	// Only msgnet-pin explicitly creates a key. A missing enrolled key never
	// causes run to generate a replacement or to register an identity.
	if _, e = os.Lstat(filepath.Join(directory, identity.MsgnetConfirmationKeyFile)); e != nil {
		return nil, noop, e
	}
	signing, e := identity.MsgnetConfirmationKey(directory)
	if e != nil {
		return nil, noop, e
	}
	ca, e := identity.ReadPrivateConfiguration(cfg.ProofCAFile, 65536)
	if e != nil {
		clear(signing)
		return nil, noop, e
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(ca) {
		clear(signing)
		return nil, noop, fmt.Errorf("native proof server CA required")
	}
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots, GetClientCertificate: func(*tls.CertificateRequestInfo) (*tls.Certificate, error) {
		current, e := identity.Load(agentDir)
		if e != nil {
			return nil, e
		}
		if current.NodeID != material.NodeID {
			return nil, fmt.Errorf("native node identity changed")
		}
		certs, e := identity.ParseCertPEM(current.CertificatePEM)
		if e != nil {
			return nil, e
		}
		chain := make([][]byte, len(certs))
		for index, c := range certs {
			chain[index] = c.Raw
		}
		return &tls.Certificate{Certificate: chain, PrivateKey: current.PrivateKey, Leaf: certs[0]}, nil
	}}
	transport := &http.Transport{TLSClientConfig: tlsConfig, Proxy: nil, DialContext: (&net.Dialer{Timeout: 3 * time.Second}).DialContext, TLSHandshakeTimeout: 3 * time.Second, ResponseHeaderTimeout: 10 * time.Second, IdleConnTimeout: 30 * time.Second}
	backend, e := msgnet.NewNodeExecution(cfg.ProofOrigin, transport, schemas)
	if e != nil {
		clear(signing)
		return nil, noop, e
	}
	installer, e := msgnet.New(msgnet.Options{Directory: directory, Tenant: pins.TenantId, Realm: pins.Realm, RootKid: cfg.RootKid, RootKey: rootKey, NodeId: material.NodeID, Pin: *selected, SigningKey: signing, Backend: backend, ResolveAuthority: backend.ResolveAuthority, Schemas: schemas, TransportSource: backend})
	close := func() { backend.Close(); transport.CloseIdleConnections(); clear(signing) }
	if e != nil {
		close()
		return nil, noop, e
	}
	return installer, close, nil
}
