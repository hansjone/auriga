package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"time"

	workspacev1 "github.com/hansjone/auriga/infra/gen/auriga/workspace/v1"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/nats-io/nkeys"
)

const sharedEntryBucket = "auriga-shared-entry"

func (config *SharedDshConfig) UnmarshalJSON(raw []byte) error {
	if len(raw) > 64*1024 {
		return fmt.Errorf("shared installation configuration too large")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	token, err := d.Token()
	if err != nil || token != json.Delim('{') {
		return fmt.Errorf("shared installation object required")
	}
	seen := map[string]bool{}
	for d.More() {
		token, err := d.Token()
		if err != nil {
			return fmt.Errorf("invalid shared installation configuration")
		}
		key, ok := token.(string)
		if !ok || seen[key] {
			return fmt.Errorf("duplicate shared installation field")
		}
		seen[key] = true
		switch key {
		case "rpc", "hop", "natsCredsFile", "natsSeedFile", "ticketPublicKeysFile", "identityKeysFile":
		default:
			return fmt.Errorf("unknown shared installation field")
		}
		var value json.RawMessage
		if d.Decode(&value) != nil {
			return fmt.Errorf("invalid shared installation value")
		}
	}
	if token, err = d.Token(); err != nil || token != json.Delim('}') || d.Decode(new(any)) != io.EOF {
		return fmt.Errorf("trailing shared installation configuration")
	}
	type plain SharedDshConfig
	var parsed plain
	d = json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err = d.Decode(&parsed); err != nil {
		return fmt.Errorf("invalid shared installation configuration")
	}
	*config = SharedDshConfig(parsed)
	return nil
}

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
	auth, err := sharedNATSAuthentication(shared)
	if err != nil {
		return nil, nil, err
	}
	originNATS := cfg.NATSURL
	if shared.NATSSeedFile != "" {
		originNATS, err = sharedNATSOrigin(cfg.NATSURL)
		if err != nil {
			return nil, nil, err
		}
	}
	if !cfg.Access.Valid() || routes == nil || originNATS == "" {
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
	nc, err := nats.Connect(originNATS, nats.Name("auriga-edge-shared-entry"),
		auth, nats.CustomInboxPrefix("_INBOX_auriga_shared_entry"), nats.Timeout(5*time.Second), nats.MaxReconnects(-1))
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

// Keep the account-JWT file adapter; a static NAS account instead uses one
// dedicated user NKey. Neither adapter inherits a password from another URL.
func sharedNATSOrigin(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u == nil || (u.Scheme != "nats" && u.Scheme != "tls") || u.Host == "" || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" {
		return "", fmt.Errorf("fixed shared NATS server origin required")
	}
	u.User = nil
	return u.String(), nil
}

func sharedNATSAuthentication(shared *SharedDshConfig) (nats.Option, error) {
	if shared == nil || (shared.NATSCredsFile == "") == (shared.NATSSeedFile == "") {
		return nil, fmt.Errorf("exactly one shared NATS credentials or user seed file required")
	}
	if shared.NATSCredsFile != "" {
		if !filepath.IsAbs(shared.NATSCredsFile) {
			return nil, fmt.Errorf("absolute shared NATS credentials file required")
		}
		return nats.UserCredentials(shared.NATSCredsFile), nil
	}
	path := shared.NATSSeedFile
	read := func() ([]byte, error) {
		if !filepath.IsAbs(path) || filepath.Clean(path) != path {
			return nil, fmt.Errorf("canonical shared replay seed path required")
		}
		for parent := filepath.Dir(path); ; parent = filepath.Dir(parent) {
			info, err := os.Lstat(parent)
			if err != nil || !info.IsDir() || (runtime.GOOS != "windows" && info.Mode().Perm()&0022 != 0 && info.Mode()&os.ModeSticky == 0) {
				return nil, fmt.Errorf("protected shared replay seed ancestors required")
			}
			if filepath.Dir(parent) == parent {
				break
			}
		}
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() || (runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0) {
			return nil, fmt.Errorf("private regular shared replay seed required")
		}
		file, err := os.Open(path)
		if err != nil {
			return nil, fmt.Errorf("shared replay seed unavailable")
		}
		defer file.Close()
		opened, err := file.Stat()
		if err != nil || !os.SameFile(info, opened) || !sharedSeedMetadata(opened) {
			return nil, fmt.Errorf("shared replay seed changed")
		}
		raw, err := io.ReadAll(io.LimitReader(file, 513))
		if err != nil || len(raw) > 512 {
			clear(raw)
			return nil, fmt.Errorf("bounded shared replay seed required")
		}
		return raw, nil
	}
	raw, err := read()
	if err != nil {
		return nil, err
	}
	user, err := nkeys.FromSeed(raw)
	if err != nil {
		clear(raw)
		return nil, fmt.Errorf("shared replay user seed is invalid")
	}
	public, err := user.PublicKey()
	user.Wipe()
	clear(raw)
	if err != nil || !nkeys.IsValidPublicUserKey(public) {
		return nil, fmt.Errorf("shared replay user NKey required")
	}
	// Use the existing library's installation adapter and bind its resolved user.
	base, err := nats.NkeyOptionFromSeed(path)
	if err != nil {
		return nil, fmt.Errorf("shared replay seed unavailable")
	}
	return func(options *nats.Options) error {
		if options.User != "" || options.Password != "" || options.Token != "" || options.UserInfo != nil || options.TokenHandler != nil || options.UserJWT != nil {
			return fmt.Errorf("mixed shared replay authentication refused")
		}
		if err := base(options); err != nil || options.Nkey != public || options.UserJWT != nil {
			return fmt.Errorf("shared replay identity changed")
		}
		// The library's original callback reopens the path without checking its
		// type/mode. Revalidate every reconnect and sign only this resolved user.
		options.SignatureCB = func(nonce []byte) ([]byte, error) {
			raw, err := read()
			if err != nil {
				return nil, err
			}
			defer clear(raw)
			current, err := nkeys.FromSeed(raw)
			if err != nil {
				return nil, fmt.Errorf("shared replay seed unavailable")
			}
			defer current.Wipe()
			resolved, err := current.PublicKey()
			if err != nil || resolved != public {
				return nil, fmt.Errorf("shared replay identity changed")
			}
			return current.Sign(nonce)
		}
		return nil
	}, nil
}
