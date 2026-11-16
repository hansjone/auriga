package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var desktopTenant = regexp.MustCompile(`^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$`)

func configureDesktop(s *state, publicFile, tenant string) error {
	if publicFile == "" && tenant == "" {
		if s.DesktopPublicKeyFingerprint == "" {
			return nil
		}
		publicFile = filepath.Join(s.Data, "config/desktop/release-public.pem")
		tenant = s.DesktopPublisherTenant
	}
	if err := desktopPhysical(publicFile); err != nil {
		return err
	}
	raw, err := smallFile(publicFile, 4096)
	if err != nil {
		return err
	}
	block, _ := pem.Decode(raw)
	if block == nil {
		return errors.New("非法桌面发行公钥")
	}
	sum := sha256.Sum256(block.Bytes)
	fingerprint := hex.EncodeToString(sum[:])
	if s.DesktopPublicKeyFingerprint != "" && (s.DesktopPublicKeyFingerprint != fingerprint || s.DesktopPublisherTenant != tenant) {
		return errors.New("桌面发行公钥或租户与安装state固定值不符")
	}
	if err := provisionDesktop(s.Data, publicFile, tenant); err != nil {
		return err
	}
	s.DesktopPublicKeyFingerprint, s.DesktopPublisherTenant = fingerprint, tenant
	return nil
}

// Public bootstrap only. The private desktop key never belongs on the platform.
// install holds its existing exclusive lock; the existing NAS renderer owns
// the desktop blob volume and configures the resource service environment.
func provisionDesktop(data, publicFile, tenant string) error {
	if publicFile == "" && tenant == "" {
		return nil
	}
	if publicFile == "" || !desktopTenant.MatchString(tenant) || tenant == "00000000-0000-0000-0000-000000000000" {
		return errors.New("桌面发行需要独立公钥及规范发行租户 UUID")
	}
	for _, path := range []string{data, publicFile, filepath.Join(data, "config/desktop/release-public.pem"), filepath.Join(data, "stack.env")} {
		if err := desktopPhysical(path); err != nil {
			return err
		}
	}
	info, err := os.Stat(publicFile)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 4096 {
		return errors.New("非法桌面发行公钥文件")
	}
	raw, err := os.ReadFile(publicFile)
	if err != nil {
		return err
	}
	block, rest := pem.Decode(raw)
	if block == nil || block.Type != "PUBLIC KEY" || len(block.Headers) != 0 || len(bytes.TrimSpace(rest)) != 0 {
		return errors.New("需要 SPKI PEM 公钥，拒绝私钥及额外材料")
	}
	key, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return errors.New("非法桌面发行公钥")
	}
	if _, ok := key.(ed25519.PublicKey); !ok {
		return errors.New("桌面发行仅支持 Ed25519 公钥")
	}
	canonical := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: block.Bytes})
	envFile := filepath.Join(data, "stack.env")
	env, err := os.ReadFile(envFile)
	if err != nil {
		return err
	}
	lines := strings.Split(string(env), "\n")
	count := 0
	for index, line := range lines {
		if strings.HasPrefix(line, "AURIGA_DESKTOP_PUBLISHER_TENANT_ID=") {
			count++
			previous := strings.TrimPrefix(line, "AURIGA_DESKTOP_PUBLISHER_TENANT_ID=")
			if previous != "" && previous != tenant {
				return errors.New("拒绝替换已有桌面发行租户")
			}
			lines[index] = "AURIGA_DESKTOP_PUBLISHER_TENANT_ID=" + tenant
		}
	}
	if count > 1 {
		return errors.New("桌面发行租户配置重复")
	}
	if count == 0 {
		lines = append(lines, "AURIGA_DESKTOP_PUBLISHER_TENANT_ID="+tenant)
	}
	directory := filepath.Join(data, "config/desktop")
	if err := os.MkdirAll(directory, 0755); err != nil {
		return err
	}
	destination := filepath.Join(directory, "release-public.pem")
	existing, err := os.ReadFile(destination)
	if err == nil {
		if !bytes.Equal(existing, canonical) {
			return errors.New("拒绝自动轮换桌面发行公钥；旧成员需受信重新安装")
		}
	} else if os.IsNotExist(err) {
		file, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
		if err != nil {
			return err
		}
		_, writeErr := file.Write(canonical)
		closeErr := file.Close()
		if err := errors.Join(writeErr, closeErr); err != nil {
			return err
		}
	} else {
		return err
	}
	// Never rewrite the active config unless the content actually changes.
	updated := []byte(strings.Join(lines, "\n"))
	if bytes.Equal(updated, env) {
		return nil
	}
	file, err := os.CreateTemp(data, ".desktop-env-")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(file.Name()) }()
	_, writeErr := file.Write(updated)
	syncErr := file.Sync()
	closeErr := file.Close()
	if err := errors.Join(writeErr, syncErr, closeErr); err != nil {
		return err
	}
	return os.Rename(file.Name(), envFile)
}

func desktopPhysical(path string) error {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	for p := absolute; ; p = filepath.Dir(p) {
		info, err := os.Lstat(p)
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		if err == nil && info.Mode()&os.ModeSymlink != 0 {
			return errors.New("拒绝间接桌面配置路径")
		}
		if p == filepath.Dir(p) {
			return nil
		}
	}
}
