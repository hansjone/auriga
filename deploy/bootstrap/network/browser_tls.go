package network

import (
	"bytes"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// BrowserTLS is an immutable, validated external TLS snapshot. It never changes
// the caller's files or installs trust. Only its digest enters receipt inputs.
type BrowserTLS struct{ Certificate, PrivateKey []byte }

func ReadBrowserTLS(certFile, keyFile string, enabled bool, hosts []string) (*BrowserTLS, error) {
	invalid := fmt.Errorf("invalid external HTTPS material")
	if !enabled {
		if certFile != "" || keyFile != "" {
			return nil, fmt.Errorf("external HTTPS inputs require direct HTTPS")
		}
		return &BrowserTLS{}, nil
	}
	read := func(path string) ([]byte, error) {
		// Only the fixed NAS installer may reopen its parent's inherited snapshots.
		fd := os.Getuid() == 10001 && (path == "/proc/self/fd/4" || path == "/proc/self/fd/5")
		before, err := os.Lstat(path)
		if err != nil || (!fd && !before.Mode().IsRegular()) {
			return nil, invalid
		}
		f, err := os.Open(path)
		if err != nil {
			return nil, invalid
		}
		defer func() { _ = f.Close() }()
		info, err := f.Stat()
		if err != nil || !info.Mode().IsRegular() || (!fd && !os.SameFile(before, info)) {
			return nil, invalid
		}
		raw, err := io.ReadAll(io.LimitReader(f, (256<<10)+1))
		after, statErr := f.Stat()
		if err != nil || statErr != nil || len(raw) > 256<<10 || int64(len(raw)) != info.Size() || info.Size() != after.Size() || !info.ModTime().Equal(after.ModTime()) {
			clear(raw)
			return nil, invalid
		}
		return raw, nil
	}
	cert, err := read(certFile)
	if err != nil {
		return nil, invalid
	}
	key, err := read(keyFile)
	if err != nil {
		return nil, invalid
	}
	// Never copy a hidden private PEM block into the public certificate leaf.
	for rest := bytes.TrimSpace(cert); len(rest) > 0; {
		block, tail := pem.Decode(rest)
		if !bytes.HasPrefix(rest, []byte("-----BEGIN CERTIFICATE-----")) || block == nil || block.Type != "CERTIFICATE" || len(block.Headers) != 0 {
			clear(key)
			return nil, invalid
		}
		rest = bytes.TrimSpace(tail)
	}
	block, tail := pem.Decode(bytes.TrimSpace(key))
	if block == nil || len(block.Headers) != 0 || len(bytes.TrimSpace(tail)) != 0 || (block.Type != "PRIVATE KEY" && block.Type != "RSA PRIVATE KEY" && block.Type != "EC PRIVATE KEY") {
		clear(key)
		return nil, invalid
	}
	pair, err := tls.X509KeyPair(cert, key)
	if err != nil || len(pair.Certificate) == 0 {
		clear(key)
		return nil, invalid
	}
	var chain []*x509.Certificate
	for _, der := range pair.Certificate {
		c, err := x509.ParseCertificate(der)
		if err != nil || time.Now().Before(c.NotBefore) || !time.Now().Before(c.NotAfter) {
			clear(key)
			return nil, invalid
		}
		chain = append(chain, c)
	}
	leaf := chain[0]
	serverAuth := len(leaf.ExtKeyUsage) == 0
	for _, usage := range leaf.ExtKeyUsage {
		serverAuth = serverAuth || usage == x509.ExtKeyUsageServerAuth || usage == x509.ExtKeyUsageAny
	}
	if leaf.IsCA || !serverAuth {
		clear(key)
		return nil, invalid
	}
	for i := 1; i < len(chain); i++ {
		if chain[i-1].CheckSignatureFrom(chain[i]) != nil {
			clear(key)
			return nil, invalid
		}
	}
	for _, host := range hosts {
		if strings.HasPrefix(host, "*.") {
			found := false
			for _, name := range leaf.DNSNames {
				found = found || strings.EqualFold(name, host)
			}
			if !found {
				clear(key)
				return nil, invalid
			}
			host = "instance" + host[1:]
		}
		if leaf.VerifyHostname(host) != nil {
			clear(key)
			return nil, invalid
		}
	}
	return &BrowserTLS{cert, key}, nil
}

func (b *BrowserTLS) Digest() string {
	if len(b.Certificate) == 0 {
		return "http-or-proxy"
	}
	return fmt.Sprintf("%x:%x", sha256.Sum256(b.Certificate), sha256.Sum256(b.PrivateKey))
}

func (b *BrowserTLS) Clear() { clear(b.PrivateKey) }

func (b *BrowserTLS) Write(out string) error {
	dir := filepath.Join(out, "browser-tls")
	if err := os.Mkdir(dir, 0700); err != nil {
		info, statErr := os.Lstat(dir)
		entries, readErr := os.ReadDir(dir)
		if !os.IsExist(err) || statErr != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || readErr != nil || len(entries) != 0 {
			return fmt.Errorf("invalid managed HTTPS directory")
		}
	}
	if len(b.Certificate) == 0 {
		return nil
	}
	for _, leaf := range []struct {
		name string
		raw  []byte
		mode os.FileMode
	}{{"browser.crt", b.Certificate, 0644}, {"browser.key", b.PrivateKey, 0600}} {
		f, err := os.OpenFile(filepath.Join(dir, leaf.name), os.O_CREATE|os.O_EXCL|os.O_WRONLY, leaf.mode)
		if err != nil {
			return fmt.Errorf("write managed HTTPS material")
		}
		_, err = f.Write(leaf.raw)
		closeErr := f.Close()
		if err != nil || closeErr != nil {
			return fmt.Errorf("write managed HTTPS material")
		}
	}
	return nil
}
