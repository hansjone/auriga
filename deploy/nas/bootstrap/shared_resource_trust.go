package main

import (
	"crypto/ed25519"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	sharedAgentResourceTrustOrigin = "https://platform:18081"
	sharedAgentResourceNotBefore   = "2020-01-01T00:00:00Z"
	sharedAgentResourceNotAfter    = "2100-01-01T00:00:00Z"
)

type sharedAgentResourceTrustKey struct {
	Kid       string `json:"kid"`
	PublicKey string `json:"publicKey"`
	Purpose   string `json:"purpose"`
	NotBefore string `json:"notBefore"`
	NotAfter  string `json:"notAfter"`
}

type sharedAgentResourceTrustConfig struct {
	SchemaVersion int                           `json:"schemaVersion"`
	Origin        string                        `json:"origin"`
	Keys          []sharedAgentResourceTrustKey `json:"keys"`
}

func validSharedAgentResourceTrust(cfg sharedAgentResourceTrustConfig) bool {
	if cfg.SchemaVersion != 1 || strings.TrimSpace(cfg.Origin) == "" || len(cfg.Keys) == 0 {
		return false
	}
	seenKeys := map[string]string{}
	for _, k := range cfg.Keys {
		if strings.TrimSpace(k.Kid) == "" {
			return false
		}
		if k.Purpose != "release" && k.Purpose != "revocation" && k.Purpose != "downgrade" {
			return false
		}
		pub, err := base64.StdEncoding.DecodeString(strings.TrimSpace(k.PublicKey))
		if err != nil || len(pub) != ed25519.PublicKeySize {
			return false
		}
		if prior, exists := seenKeys[string(pub)]; exists && prior != k.Purpose {
			return false
		}
		seenKeys[string(pub)] = k.Purpose
		start, e1 := time.Parse(time.RFC3339Nano, strings.TrimSpace(k.NotBefore))
		end, e2 := time.Parse(time.RFC3339Nano, strings.TrimSpace(k.NotAfter))
		if e1 != nil || e2 != nil || !start.Before(end) {
			return false
		}
	}
	return true
}

func checkSharedNoSymlinkSegments(base string, parts ...string) error {
	cur := base
	for _, p := range parts {
		cur = filepath.Join(cur, p)
		info, err := os.Lstat(cur)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlink refused in path: %s", cur)
		}
	}
	return nil
}

func buildSharedAgentResourceTrust(rootsRaw []byte, origin string) ([]byte, error) {
	var roots map[string]map[string]string
	if err := json.Unmarshal(rootsRaw, &roots); err != nil {
		return nil, fmt.Errorf("parse resource-roots.json: %w", err)
	}
	origin = strings.TrimSpace(origin)
	if origin == "" {
		origin = sharedAgentResourceTrustOrigin
	}
	seenKeys := map[string]string{}
	keys := make([]sharedAgentResourceTrustKey, 0, 3)
	for _, purpose := range []string{"release", "revocation", "downgrade"} {
		entry := roots[purpose]
		kid := strings.TrimSpace(entry["kid"])
		spkiB64 := strings.TrimSpace(entry["publicKeySpkiBase64"])
		if kid == "" || spkiB64 == "" {
			return nil, fmt.Errorf("resource-roots.json missing %s key", purpose)
		}
		spki, err := base64.StdEncoding.DecodeString(spkiB64)
		if err != nil {
			return nil, fmt.Errorf("decode %s publicKeySpkiBase64: %w", purpose, err)
		}
		parsed, err := x509.ParsePKIXPublicKey(spki)
		if err != nil {
			return nil, fmt.Errorf("parse %s SPKI public key: %w", purpose, err)
		}
		pub, ok := parsed.(ed25519.PublicKey)
		if !ok || len(pub) != ed25519.PublicKeySize {
			return nil, fmt.Errorf("%s key must be Ed25519", purpose)
		}
		rawKey := string(pub)
		if prior, exists := seenKeys[rawKey]; exists {
			return nil, fmt.Errorf("resource-roots.json reuses public key between %s and %s", prior, purpose)
		}
		seenKeys[rawKey] = purpose
		keys = append(keys, sharedAgentResourceTrustKey{
			Kid:       kid,
			PublicKey: base64.StdEncoding.EncodeToString(pub),
			Purpose:   purpose,
			NotBefore: sharedAgentResourceNotBefore,
			NotAfter:  sharedAgentResourceNotAfter,
		})
	}
	out, err := json.MarshalIndent(sharedAgentResourceTrustConfig{
		SchemaVersion: 1,
		Origin:        origin,
		Keys:          keys,
	}, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}

// ensureSharedAgentResourceTrust projects <dataRoot>/resource-keys/resource-roots.json
// to <dataRoot>/data/agent/agent/resource-trust.json when the site has resource roots
// and no valid agent trust file yet.
func ensureSharedAgentResourceTrust(dataRoot string) error {
	if err := checkSharedNoSymlinkSegments(dataRoot, "data", "agent", "agent", "resource-trust.json"); err != nil {
		return err
	}
	if err := checkSharedNoSymlinkSegments(dataRoot, "resource-keys", "resource-roots.json"); err != nil {
		return err
	}
	rootsPath := filepath.Join(dataRoot, "resource-keys", "resource-roots.json")
	trustDir := filepath.Join(dataRoot, "data", "agent", "agent")
	trustPath := filepath.Join(trustDir, "resource-trust.json")

	if info, err := os.Lstat(trustPath); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return fmt.Errorf("agent resource-trust.json must be a regular file")
		}
		raw, err := os.ReadFile(trustPath)
		if err != nil {
			return err
		}
		var existing sharedAgentResourceTrustConfig
		if err := json.Unmarshal(raw, &existing); err != nil {
			return fmt.Errorf("invalid agent resource-trust.json: %w", err)
		}
		if validSharedAgentResourceTrust(existing) {
			return nil
		}
		// Existing file is incomplete (e.g. empty or invalid keys); fall through to project from roots if available.
	} else if !os.IsNotExist(err) {
		return err
	}

	rootsInfo, err := os.Lstat(rootsPath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if rootsInfo.Mode()&os.ModeSymlink != 0 || !rootsInfo.Mode().IsRegular() {
		return fmt.Errorf("resource-roots.json must be a regular file")
	}
	rootsRaw, err := os.ReadFile(rootsPath)
	if err != nil {
		return err
	}
	payload, err := buildSharedAgentResourceTrust(rootsRaw, sharedAgentResourceTrustOrigin)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(trustDir, 0755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(trustDir, ".resource-trust-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if err := tmp.Chmod(0644); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(payload); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, trustPath)
}
