package app

import (
	"bytes"
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
	options := msgnet.Options{Directory: filepath.Join(agentDir, "msgnet-state"), Tenant: pins.TenantId, Realm: pins.Realm, RootKid: cfg.RootKid, RootKey: rootKey, NodeId: material.NodeID, Pin: *selected, SigningKey: signing, Backend: backend, ResolveAuthority: backend.ResolveAuthority, Schemas: schemas, TransportSource: backend}
	if e = importMsgnetState(agentDir, options); e != nil {
		backend.Close()
		transport.CloseIdleConnections()
		clear(signing)
		return nil, noop, e
	}
	installer, e := msgnet.New(options)
	close := func() { backend.Close(); transport.CloseIdleConnections(); clear(signing) }
	if e != nil {
		close()
		return nil, noop, e
	}
	return installer, close, nil
}

// Pre-existing identity directories remain intact. Publish the verified legacy
// journal once, without overwriting a concurrent winner in the per-node ledger.
func importMsgnetState(agentDir string, options msgnet.Options) error {
	if e := identity.EnsureDir(options.Directory); e != nil {
		return e
	}
	journal := filepath.Join(options.Directory, "control-state.json")
	if _, e := os.Lstat(journal); e == nil {
		return nil
	} else if !os.IsNotExist(e) {
		return e
	}
	entries, e := os.ReadDir(filepath.Join(agentDir, "msgnet-identities"))
	if os.IsNotExist(e) {
		return nil
	}
	if e != nil {
		return e
	}
	var saved []byte
	for _, entry := range entries {
		if !entry.IsDir() {
			return fmt.Errorf("invalid legacy confirmation directory")
		}
		dir := filepath.Join(agentDir, "msgnet-identities", entry.Name())
		path := filepath.Join(dir, "control-state.json")
		if _, e = os.Lstat(path); os.IsNotExist(e) {
			continue
		} else if e != nil {
			return e
		}
		raw, e := identity.ReadPrivateConfiguration(path, 32<<20)
		if e != nil {
			return e
		}
		legacy := options
		legacy.Directory = dir
		if _, e = msgnet.New(legacy); e != nil {
			return fmt.Errorf("legacy node ledger validation: %w", e)
		}
		if saved != nil && !bytes.Equal(saved, raw) {
			return fmt.Errorf("conflicting legacy node control ledgers")
		}
		saved = raw
	}
	if saved == nil {
		return nil
	}
	temporary, e := os.CreateTemp(options.Directory, ".control-import-")
	if e != nil {
		return e
	}
	name := temporary.Name()
	defer func() { _ = os.Remove(name) }()
	if e = temporary.Close(); e != nil {
		return e
	}
	if e = identity.WritePrivateSnapshot(name, saved); e != nil {
		return e
	}
	if e = os.Link(name, journal); e != nil && !os.IsExist(e) {
		return e
	}
	// The normal Installer load revalidates whichever complete journal won.
	return nil
}
