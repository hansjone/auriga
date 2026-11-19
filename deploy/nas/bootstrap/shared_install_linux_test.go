package main

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestSharedInstallationLinuxMetadataRejectsLinksOwnersModesAndUnknownLeaves(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Fatal("this pure metadata fixture requires root; no installation service is contacted")
	}
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(root, "shared-source")
	if err := os.Mkdir(out, 0700); err != nil {
		t.Fatal(err)
	}
	unlock, err := sharedInstallationLock(out)
	if err != nil {
		t.Fatal(err)
	}
	fd, err := unix.Open(filepath.Join(out, ".lock"), unix.O_RDWR|unix.O_NOFOLLOW, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); err == nil {
		unix.Close(fd)
		unlock()
		t.Fatal("installation lock was not exclusive")
	}
	unix.Close(fd)
	unlock()
	if err := os.Chmod(filepath.Join(out, ".lock"), 0640); err != nil {
		t.Fatal(err)
	}
	if unlock, err := sharedInstallationLock(out); err == nil {
		unlock()
		t.Fatal("broad lock permissions accepted")
	}
	base := filepath.Join(root, "materials")
	if err := os.Mkdir(base, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chown(base, 10001, 10001); err != nil {
		t.Fatal(err)
	}
	leaf := filepath.Join(base, "identity-keys.json")
	want := []byte(`{"keys":["synthetic-public-data"]}`)
	if err := os.WriteFile(leaf, want, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chown(leaf, 10001, 10001); err != nil {
		t.Fatal(err)
	}
	got, err := sharedBasePublic(base, "identity-keys.json")
	if err != nil || !bytes.Equal(got, want) {
		t.Fatal("fixed sealed public leaf rejected", err)
	}
	if err := os.Link(leaf, filepath.Join(root, "hardlink")); err != nil {
		t.Fatal(err)
	}
	if _, err := sharedBasePublic(base, "identity-keys.json"); err == nil {
		t.Fatal("hardlinked public source accepted")
	}
	if err := os.Remove(filepath.Join(root, "hardlink")); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(leaf, 0664); err != nil {
		t.Fatal(err)
	}
	if _, err := sharedBasePublic(base, "identity-keys.json"); err == nil {
		t.Fatal("writable public source accepted")
	}
	if err := os.Chmod(leaf, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chown(leaf, 0, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := sharedBasePublic(base, "identity-keys.json"); err == nil {
		t.Fatal("reowned sealed base source accepted")
	}
	if _, err := sharedBasePublic(base, "pki/ca.key"); err == nil {
		t.Fatal("private CA read was allowed")
	}
	if _, err := sharedBasePublic(base, "uac/adapter.json"); err == nil {
		t.Fatal("private adapter treated as public")
	}
	if err := os.Remove(leaf); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "hardlink"), leaf); err != nil {
		t.Fatal(err)
	}
	if _, err := sharedBasePublic(base, "identity-keys.json"); err == nil {
		t.Fatal("symlink public source accepted")
	}
}

func TestSharedInstallationEnrolledNodeBindingSurvivesRenewalAndRejectsWrongAuthority(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Fatal("this pure public-certificate fixture requires root")
	}
	dir := t.TempDir()
	base := filepath.Join(dir, "config/materials")
	for _, path := range []string{filepath.Join(dir, "config"), base, filepath.Join(base, "pki"), filepath.Join(dir, "data"), filepath.Join(dir, "data/agent"), filepath.Join(dir, "data/agent/agent")} {
		if err := os.Mkdir(path, 0755); err != nil {
			t.Fatal(err)
		}
	}
	for _, path := range []string{base, filepath.Join(base, "pki")} {
		if err := os.Chown(path, 10001, 10001); err != nil {
			t.Fatal(err)
		}
	}
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	ca := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "synthetic-shared-install-ca"}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	caDER, err := x509.CreateCertificate(rand.Reader, ca, ca, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	caPath := filepath.Join(base, "pki/ca.crt")
	if err := os.WriteFile(caPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chown(caPath, 10001, 10001); err != nil {
		t.Fatal(err)
	}
	nodeKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	nodeID := "11111111-2222-4333-8444-555555555555"
	uri, _ := url.Parse("auriga://node/" + nodeID)
	node := &x509.Certificate{SerialNumber: big.NewInt(2), NotBefore: now.Add(-time.Minute), NotAfter: now.Add(30 * time.Minute), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}, URIs: []*url.URL{uri}}
	write := func(c *x509.Certificate) {
		t.Helper()
		der, err := x509.CreateCertificate(rand.Reader, c, ca, &nodeKey.PublicKey, caKey)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "data/agent/agent/node.crt"), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(node)
	id, fingerprint, err := sharedNodeIdentity(base)
	if err != nil || id != nodeID || len(fingerprint) != 64 {
		t.Fatal("enrolled public identity rejected", err)
	}
	node.SerialNumber = big.NewInt(3)
	node.NotAfter = now.Add(45 * time.Minute)
	write(node)
	id, renewed, err := sharedNodeIdentity(base)
	if err != nil || id != nodeID || renewed != fingerprint {
		t.Fatal("same Node certificate renewal changed stable installation binding", err)
	}
	for name, change := range map[string]func(*x509.Certificate){
		"expired":        func(c *x509.Certificate) { c.NotAfter = now.Add(-time.Second) },
		"future":         func(c *x509.Certificate) { c.NotBefore = now.Add(time.Minute) },
		"server-only":    func(c *x509.Certificate) { c.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth} },
		"ambiguous-node": func(c *x509.Certificate) { c.URIs = []*url.URL{uri, uri} },
		"non-node":       func(c *x509.Certificate) { u, _ := url.Parse("auriga://edge/synthetic"); c.URIs = []*url.URL{u} },
	} {
		t.Run(name, func(t *testing.T) {
			altered := *node
			change(&altered)
			write(&altered)
			if _, _, err := sharedNodeIdentity(base); err == nil {
				t.Fatal("invalid enrolled Node certificate accepted")
			}
		})
	}
	write(node)
	otherKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	otherDER, err := x509.CreateCertificate(rand.Reader, ca, ca, &otherKey.PublicKey, otherKey)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(caPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: otherDER}), 0644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := sharedNodeIdentity(base); err == nil {
		t.Fatal("certificate from a different installation authority accepted")
	}
}
