package main

import (
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
)

func validateResourceMaterial(root string) error {
	if err := checkResourceMaterial(root); err != nil {
		return fmt.Errorf("残缺或损坏的资源密钥目录 %s；不得覆盖/重新生成已使用密钥。请保留目录，从原备份恢复；仅确认从未使用时人工隔离整个目录后重跑install：%w", root, err)
	}
	return nil
}

func checkResourceMaterial(root string) error {
	if err := noSymlinks(root); err != nil {
		return err
	}
	var roots map[string]map[string]string
	if err := readJSON(filepath.Join(root, "resource-roots.json"), &roots); err != nil {
		return err
	}
	for _, role := range []string{"release", "revocation", "downgrade"} {
		part := "platform"
		if role == "downgrade" {
			part = "offline"
		}
		file := filepath.Join(root, part, role+".pk8")
		if err := noSymlinks(file); err != nil {
			return err
		}
		raw, err := smallFile(file, 64<<10)
		if err != nil {
			return err
		}
		key, err := x509.ParsePKCS8PrivateKey(raw)
		if err != nil {
			return err
		}
		private, ok := key.(ed25519.PrivateKey)
		if !ok {
			return fmt.Errorf("%s需要Ed25519", role)
		}
		public := private.Public().(ed25519.PublicKey)
		der, err := x509.MarshalPKIXPublicKey(public)
		if err != nil {
			return err
		}
		hash := sha256.Sum256(public)
		if roots[role]["publicKeySpkiBase64"] != base64.StdEncoding.EncodeToString(der) || roots[role]["kid"] != "site-"+hex.EncodeToString(hash[:8])+"-"+role {
			return fmt.Errorf("%s公私钥不对应", role)
		}
	}
	for _, name := range []string{"platform", "offline"} {
		if err := noSymlinks(filepath.Join(root, name)); err != nil {
			return err
		}
		info, err := os.Stat(filepath.Join(root, name))
		if err != nil {
			return err
		}
		if !info.IsDir() {
			return fmt.Errorf("%s不是目录", name)
		}
	}
	return nil
}
