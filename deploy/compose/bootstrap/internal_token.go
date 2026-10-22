package main

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
)

const iamRPCPort = 9091

// Only the platform receives this private directory. No raw secret goes into JSON or env.
// Preserve an existing installation on a repeated bootstrap instead of rotating its credentials.
func writeInternalTokenInstallation(out string) error {
	dir := filepath.Join(out, "iam-installation")
	if err := os.Mkdir(dir, 0o700); err != nil && !os.IsExist(err) {
		return fmt.Errorf("create private IAM installation directory")
	}
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || runtime.GOOS != "windows" && info.Mode().Perm() != 0o700 {
		return fmt.Errorf("invalid private IAM installation directory")
	}
	secretPath := filepath.Join(dir, "edge.secret")
	secret, err := readPrivate(secretPath)
	defer func() { clear(secret) }()
	if os.IsNotExist(err) {
		raw := make([]byte, 32)
		if _, err := rand.Read(raw); err != nil {
			return fmt.Errorf("generate IAM installation credential")
		}
		secret = []byte(base64.RawURLEncoding.EncodeToString(raw))
		clear(raw)
		if err := createPrivate(secretPath, secret); err != nil {
			return err
		}
	} else if err != nil {
		return fmt.Errorf("read IAM installation credential")
	}
	info, err = os.Lstat(secretPath)
	decoded, decodeErr := base64.RawURLEncoding.DecodeString(string(secret))
	defer clear(decoded)
	if err != nil || !info.Mode().IsRegular() || len(secret) != 43 || decodeErr != nil || len(decoded) != 32 || runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		return fmt.Errorf("invalid IAM installation credential")
	}
	hash := sha256.Sum256(secret)
	properties := []byte("auriga.iam.internal-secret-hashes.edge=" + hex.EncodeToString(hash[:]) + "\nauriga.iam.edge-token-export.installation-owner=" + fmt.Sprint(os.Getuid()) + "\n")
	path := filepath.Join(dir, "internal.properties")
	existing, err := readPrivate(path)
	if os.IsNotExist(err) {
		return createPrivate(path, properties)
	}
	info, statErr := os.Lstat(path)
	if err != nil || statErr != nil || !info.Mode().IsRegular() || string(existing) != string(properties) || runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		return fmt.Errorf("invalid IAM installation hash configuration")
	}
	return nil
}

func readPrivate(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() < 1 || info.Size() > 256 || runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		return nil, fmt.Errorf("invalid private IAM installation file")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open private IAM installation file")
	}
	defer func() { _ = f.Close() }()
	opened, err := f.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return nil, fmt.Errorf("changed private IAM installation file")
	}
	raw, err := io.ReadAll(io.LimitReader(f, 257))
	if err != nil || len(raw) > 256 {
		clear(raw)
		return nil, fmt.Errorf("read private IAM installation file")
	}
	return raw, nil
}

func createPrivate(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create private IAM installation file")
	}
	_, writeErr := f.Write(data)
	closeErr := f.Close()
	if writeErr != nil || closeErr != nil {
		return fmt.Errorf("write private IAM installation file")
	}
	return nil
}
