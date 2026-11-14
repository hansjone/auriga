package identity

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"fmt"
	"os"
	"path/filepath"
)

const MsgnetConfirmationKeyFile = "msgnet-confirmation.pk8"

// MsgnetConfirmationKey is independent of native node.key (ECDSA) and sealing
// credentials. Its public SPKI must be enrolled by D in NodeControlPins. Merely
// creating this file does not grant admission or register an identity.
func MsgnetConfirmationKey(dir string) (ed25519.PrivateKey, error) {
	lock := identityIOLock(dir)
	lock.Lock()
	defer lock.Unlock()
	if !filepath.IsAbs(dir) || filepath.Clean(dir) != dir {
		return nil, fmt.Errorf("absolute node identity directory required")
	}
	for p := dir; ; p = filepath.Dir(p) {
		info, err := os.Lstat(p)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("invalid node identity directory")
		}
		if filepath.Dir(p) == p {
			break
		}
	}
	path := filepath.Join(dir, MsgnetConfirmationKeyFile)
	load := func() (ed25519.PrivateKey, error) {
		info, err := os.Lstat(path)
		if err != nil {
			return nil, err
		}
		if !info.Mode().IsRegular() || info.Size() > 4096 || checkPrivateKeyFile(path) != nil {
			return nil, fmt.Errorf("invalid confirmation key file")
		}
		f, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		defer func() { _ = f.Close() }()
		opened, err := f.Stat()
		if err != nil || !os.SameFile(info, opened) {
			return nil, fmt.Errorf("confirmation key changed")
		}
		raw := make([]byte, info.Size())
		if _, err = f.ReadAt(raw, 0); err != nil {
			return nil, err
		}
		defer clear(raw)
		v, err := x509.ParsePKCS8PrivateKey(raw)
		if err != nil {
			return nil, fmt.Errorf("invalid confirmation key encoding")
		}
		key, ok := v.(ed25519.PrivateKey)
		if !ok {
			return nil, fmt.Errorf("confirmation key must be Ed25519")
		}
		return key, nil
	}
	key, err := load()
	if err == nil || !os.IsNotExist(err) {
		return key, err
	}
	_, generated, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	defer clear(generated)
	raw, err := x509.MarshalPKCS8PrivateKey(generated)
	if err != nil {
		return nil, err
	}
	defer clear(raw)
	f, err := os.CreateTemp(dir, ".msgnet-confirmation-")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.Remove(f.Name()) }()
	if err = restrictFile(f.Name()); err == nil {
		_, err = f.Write(raw)
	}
	if err == nil {
		err = syncTrustGuard(f, dir)
	}
	closeErr := f.Close()
	if err != nil {
		return nil, err
	}
	if closeErr != nil {
		return nil, closeErr
	}
	// Link publishes a fully written file without replacing a winner from another
	// process. Neither a crash nor concurrent initialization can rotate this key.
	if err = os.Link(f.Name(), path); err != nil && !os.IsExist(err) {
		return nil, err
	}
	guard, err := os.OpenFile(path, os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	err = syncTrustGuard(guard, dir)
	closeErr = guard.Close()
	if err != nil {
		return nil, err
	}
	if closeErr != nil {
		return nil, closeErr
	}
	return load()
}
