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
	defaultAgentResourceTrustOrigin = "https://platform:18081"
	defaultAgentResourceNotBefore   = "2020-01-01T00:00:00Z"
	defaultAgentResourceNotAfter    = "2100-01-01T00:00:00Z"
)

type agentResourceTrustKey struct {
	Kid       string `json:"kid"`
	PublicKey string `json:"publicKey"`
	Purpose   string `json:"purpose"`
	NotBefore string `json:"notBefore"`
	NotAfter  string `json:"notAfter"`
}

type agentResourceTrustConfig struct {
	SchemaVersion int                     `json:"schemaVersion"`
	Origin        string                  `json:"origin"`
	Keys          []agentResourceTrustKey `json:"keys"`
}

func validAgentResourceTrust(cfg agentResourceTrustConfig) bool {
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

func checkNoSymlinkSegments(base string, parts ...string) error {
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
			return fmt.Errorf("拒绝符号链接路径: %s", cur)
		}
	}
	return nil
}

func buildAgentResourceTrust(roots map[string]map[string]string, origin string) ([]byte, error) {
	origin = strings.TrimSpace(origin)
	if origin == "" {
		origin = defaultAgentResourceTrustOrigin
	}
	seenKeys := map[string]string{}
	keys := make([]agentResourceTrustKey, 0, 3)
	for _, purpose := range []string{"release", "revocation", "downgrade"} {
		entry := roots[purpose]
		kid := strings.TrimSpace(entry["kid"])
		spkiB64 := strings.TrimSpace(entry["publicKeySpkiBase64"])
		if kid == "" || spkiB64 == "" {
			return nil, fmt.Errorf("resource-roots.json 缺少 %s 公钥材料", purpose)
		}
		spki, err := base64.StdEncoding.DecodeString(spkiB64)
		if err != nil {
			return nil, fmt.Errorf("resource-roots.json %s 公钥解码失败: %w", purpose, err)
		}
		parsed, err := x509.ParsePKIXPublicKey(spki)
		if err != nil {
			return nil, fmt.Errorf("resource-roots.json %s SPKI 解析失败: %w", purpose, err)
		}
		pub, ok := parsed.(ed25519.PublicKey)
		if !ok || len(pub) != ed25519.PublicKeySize {
			return nil, fmt.Errorf("resource-roots.json %s 需要 Ed25519 公钥", purpose)
		}
		rawKey := string(pub)
		if prior, exists := seenKeys[rawKey]; exists {
			return nil, fmt.Errorf("resource-roots.json %s 与 %s 复用了同一公钥", purpose, prior)
		}
		seenKeys[rawKey] = purpose
		keys = append(keys, agentResourceTrustKey{
			Kid:       kid,
			PublicKey: base64.StdEncoding.EncodeToString(pub),
			Purpose:   purpose,
			NotBefore: defaultAgentResourceNotBefore,
			NotAfter:  defaultAgentResourceNotAfter,
		})
	}
	raw, err := json.MarshalIndent(agentResourceTrustConfig{
		SchemaVersion: 1,
		Origin:        origin,
		Keys:          keys,
	}, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(raw, '\n'), nil
}

func ensureAgentResourceTrust(dataRoot string, roots map[string]map[string]string) error {
	if err := checkNoSymlinkSegments(dataRoot, "data", "agent", "agent", "resource-trust.json"); err != nil {
		return err
	}
	agentDir := filepath.Join(dataRoot, "data", "agent", "agent")
	trustPath := filepath.Join(agentDir, "resource-trust.json")
	if info, err := os.Lstat(trustPath); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return fmt.Errorf("agent resource-trust.json 必须是普通文件: %s", trustPath)
		}
		var existing agentResourceTrustConfig
		if err := readJSON(trustPath, &existing); err != nil {
			return fmt.Errorf("已有 agent resource-trust.json 无法解析: %w", err)
		}
		if validAgentResourceTrust(existing) {
			return nil
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.MkdirAll(agentDir, 0755); err != nil {
		return err
	}
	if err := noSymlinks(agentDir); err != nil {
		return err
	}
	payload, err := buildAgentResourceTrust(roots, defaultAgentResourceTrustOrigin)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(agentDir, ".resource-trust-*")
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
