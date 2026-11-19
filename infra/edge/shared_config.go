package main

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	workspacev1 "github.com/hansjone/auriga/infra/gen/auriga/workspace/v1"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

const sharedEntryBucket = "auriga-shared-entry"

func sharedKeyFile(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("shared key file must be a readable regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("shared key file unavailable")
	}
	defer func() { _ = file.Close() }()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) || !opened.Mode().IsRegular() {
		return nil, fmt.Errorf("shared key file changed while opening")
	}
	raw, err := io.ReadAll(io.LimitReader(file, 65537))
	if err != nil || len(raw) > 65536 {
		return nil, fmt.Errorf("shared key file exceeds its fixed size limit")
	}
	return raw, nil
}

func loadSharedTicketKeys(path string) (map[string]ed25519.PublicKey, error) {
	raw, err := sharedKeyFile(path)
	if err != nil {
		return nil, err
	}
	var file jwkKeyFile
	if err := json.Unmarshal(raw, &file); err != nil || len(file.Keys) == 0 || len(file.Keys) > 16 {
		return nil, fmt.Errorf("shared ticket keys must be a bounded public JWK set")
	}
	keys := make(map[string]ed25519.PublicKey)
	for _, key := range file.Keys {
		public, err := base64.RawURLEncoding.DecodeString(key.X)
		if err != nil || len(public) != ed25519.PublicKeySize || key.D != "" ||
			key.Kty != "OKP" || key.Crv != "Ed25519" || key.Alg != "EdDSA" ||
			key.Use != "placement-ticket" || key.Kid == "" || len(key.Kid) > 128 || keys[key.Kid] != nil {
			return nil, fmt.Errorf("invalid or duplicate shared ticket verification key")
		}
		keys[key.Kid] = ed25519.PublicKey(public)
	}
	return keys, nil
}

func openSharedEntry(cfg Config, routes RouteStore, ownerSigner IdentitySigner) (*SharedEntry, func(), error) {
	if cfg.SharedDsh == nil {
		return nil, func() {}, nil
	}
	shared := cfg.SharedDsh
	if !cfg.Access.Valid() || routes == nil || cfg.NATSURL == "" || shared.NATSCredsFile == "" {
		return nil, nil, fmt.Errorf("shared entry requires installation access, routes and dedicated NATS credentials")
	}
	origin := cfg.Access.PortalOrigin()
	if origin.Scheme != "https" && (cfg.Access.Mode() != "ip" || !cfg.Access.InsecureHTTPAcknowledged() || origin.Scheme != "http") {
		return nil, nil, fmt.Errorf("shared entry requires HTTPS or explicitly acknowledged IP HTTP")
	}
	keys, err := loadSharedTicketKeys(shared.TicketPublicKeysFile)
	if err != nil {
		return nil, nil, err
	}
	if _, err := sharedKeyFile(shared.IdentityKeysFile); err != nil {
		return nil, nil, err
	}
	signer, err := LoadIdentitySigner(shared.IdentityKeysFile)
	if err != nil {
		return nil, nil, err
	}
	if ownerSigner != nil && ownerSigner.PublicKey().Equal(signer.PublicKey()) {
		return nil, nil, fmt.Errorf("shared ingress and legacy identity signing keys must have separate purposes")
	}
	for _, public := range keys {
		if public.Equal(signer.PublicKey()) {
			return nil, nil, fmt.Errorf("shared ticket and ingress signing keys must have separate purposes")
		}
	}
	rpc, err := newIAMRPC(shared.RPC, shared.Hop)
	if err != nil {
		return nil, nil, err
	}
	nc, err := nats.Connect(cfg.NATSURL, nats.Name("auriga-edge-shared-entry"),
		nats.UserCredentials(shared.NATSCredsFile), nats.Timeout(5*time.Second), nats.MaxReconnects(-1))
	if err != nil {
		_ = rpc.Close()
		return nil, nil, fmt.Errorf("shared replay ledger connection unavailable")
	}
	close := func() { nc.Close(); _ = rpc.Close() }
	js, err := jetstream.New(nc)
	if err != nil {
		close()
		return nil, nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	kv, err := js.KeyValue(ctx, sharedEntryBucket)
	if errors.Is(err, jetstream.ErrBucketNotFound) {
		// The installer explicitly grants this identity only the dedicated
		// bucket. Creating it never broadens browser or user access.
		kv, err = js.CreateKeyValue(ctx, jetstream.KeyValueConfig{
			Bucket: sharedEntryBucket, TTL: 12 * time.Hour, Storage: jetstream.FileStorage,
		})
	}
	if err != nil {
		close()
		return nil, nil, fmt.Errorf("shared replay ledger unavailable")
	}
	status, err := kv.Status(ctx)
	if err != nil || status.Config().Storage != jetstream.FileStorage || status.TTL() > 0 && status.TTL() < 12*time.Hour {
		close()
		return nil, nil, fmt.Errorf("shared replay ledger retention is shorter than browser bindings")
	}
	return &SharedEntry{
		Authority: &sharedWorkspaceAuthority{rpc: rpc, client: workspacev1.NewSharedDshServiceClient(rpc.conn), routes: routes, tenant: cfg.Access.TenantID()},
		Store:     &NATSSharedEntryStore{KV: kv}, TicketKeys: keys,
		IdentityKey: signer.priv, IdentityKid: signer.Kid(), LiveModes: true,
	}, close, nil
}
