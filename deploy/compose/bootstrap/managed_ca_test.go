package main

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestManagedCAValidation(t *testing.T) {
	a := customAccess("domain")
	a.PortalOrigin.Scheme = "https"
	a.AdminOrigin.Scheme = "https"
	a.IssuerOrigin = a.PortalOrigin
	a.Listeners.Portal.Tls = true
	a.Listeners.Admin.Tls = true
	a.InsecureHttpAcknowledged = false
	a.Instances.AccessInstancesSubdomain.Scheme = "https"
	a.Listeners.Portal.Port = a.PortalOrigin.Port
	_, access, err := marshalAccess(a)
	if err != nil {
		t.Fatal(err)
	}
	key, ca, root := mustCA()
	intermediateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := *ca
	tmpl.SerialNumber = big.NewInt(2)
	tmpl.Subject.CommonName = "local test intermediate"
	tmpl.RawSubject = nil
	tmpl.SubjectKeyId = nil
	intermediateDER, err := x509.CreateCertificate(rand.Reader, &tmpl, ca, &intermediateKey.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	intermediate, err := x509.ParseCertificate(intermediateDER)
	if err != nil {
		t.Fatal(err)
	}
	intermediatePEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: intermediateDER})
	hosts := []string{a.PortalOrigin.Host, a.AdminOrigin.Host, "*." + a.Instances.AccessInstancesSubdomain.ParentDomain}
	leaf, privateKey := mustIssue(intermediateKey, intermediate, "local HTTPS", "auriga://test/browser", hosts, nil, []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth})
	serverChain := bytes.Join([][]byte{leaf, intermediatePEM}, nil)
	_, _, unrelated := mustCA()
	expired := *ca
	expired.NotBefore = time.Now().Add(-2 * time.Hour)
	expired.NotAfter = time.Now().Add(-time.Hour)
	expiredDER, err := x509.CreateCertificate(rand.Reader, &expired, &expired, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	expiredPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: expiredDER})
	for _, tc := range []struct {
		name string
		raw  []byte
		want string
	}{
		{"valid", root, ""},
		{"valid-bundle", bytes.Join([][]byte{unrelated, root}, []byte("\r\n")), ""},
		{"intermediate-only", intermediatePEM, "requires a self-signed root"},
		{"empty", nil, "invalid public managed CA bundle"},
		{"oversized", bytes.Repeat([]byte("x"), managedCALimit+1), "invalid public managed CA bundle"},
		{"leaf", leaf, "invalid public managed CA bundle"},
		{"private-key", bytes.Join([][]byte{root, privateKey}, nil), "invalid public managed CA bundle"},
		{"nested-malformed", bytes.Join([][]byte{[]byte("-----BEGIN CERTIFICATE-----\nbad\n"), root}, nil), "invalid public managed CA bundle"},
		{"expired", expiredPEM, "invalid public managed CA bundle"},
		{"unrelated", unrelated, "browser HTTPS chain does not verify"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "test-ca.pem")
			if err := os.WriteFile(path, tc.raw, 0600); err != nil {
				t.Fatal(err)
			}
			got, err := readManagedCA(path, access, serverChain)
			if tc.want != "" {
				if err == nil || !strings.Contains(err.Error(), tc.want) || got != nil {
					t.Fatalf("unexpected validation result: %v", err)
				}
			} else if err != nil || !bytes.Equal(got, tc.raw) {
				t.Fatalf("valid public CA refused or changed: %v", err)
			}
		})
	}
	_, httpAccess, err := marshalAccess(customAccess("domain"))
	if err != nil {
		t.Fatal(err)
	}
	if err := validateManagedCAAccess(httpAccess); err == nil || !strings.Contains(err.Error(), "direct HTTPS") {
		t.Fatalf("managed CA accepted for HTTP: %v", err)
	}
	a.Listeners.Portal.Port--
	_, mismatched, err := marshalAccess(a)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateManagedCAAccess(mismatched); err == nil || !strings.Contains(err.Error(), "matching published and edge portal ports") {
		t.Fatalf("managed CA accepted with unreachable instance-facing port: %v", err)
	}
}

func TestManagedCASealedInstall(t *testing.T) {
	f := activationFixtureWithCA(t, true)
	before := originalBase(t, f.out)
	run := func(args ...string) (string, error) {
		b, err := exec.Command(f.binary, args...).CombinedOutput()
		return string(b), err
	}
	ca := f.installArgs[len(f.installArgs)-1]
	source, err := os.ReadFile(ca)
	if err != nil {
		t.Fatal(err)
	}
	if b, err := run(f.installArgs...); err != nil {
		t.Fatalf("existing managed CA install: %v %s", err, b)
	}
	if b, err := run(append(f.args, "-validate-access")...); err != nil {
		t.Fatalf("managed CA base refused by activation preflight: %v %s", err, b)
	}
	if b, err := run("-print-compose-recipe", "-out", f.out); err != nil || b != "public-test managed-ca\n" {
		t.Fatalf("sealed CA recipe selection: %v %s", err, b)
	}
	if b, err := run(f.installArgs[:len(f.installArgs)-2]...); err == nil || !strings.Contains(b, "installation inputs or identity materials changed") {
		t.Fatalf("omitting CA rewrote a sealed installation: %v %s", err, b)
	}
	if b, err := run(append(f.args, "-validate-access", "-managed-ca-file", ca)...); err == nil || !strings.Contains(b, "base installation input") {
		t.Fatalf("activation accepted an alternate CA input: %v %s", err, b)
	}
	assertBase(t, f.out, before)
	actual, err := os.ReadFile(filepath.Join(f.out, managedCAFile))
	if err != nil || !bytes.Equal(actual, source) {
		t.Fatal("mounted CA does not equal the installer input")
	}
	for _, file := range []string{managedCAFile, managedCARecipe} {
		t.Run("reject-drift/"+file, func(t *testing.T) {
			path := filepath.Join(f.out, file)
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, append(bytes.Clone(raw), '\n'), 0644); err != nil {
				t.Fatal(err)
			}
			if b, err := run("-print-compose-recipe", "-out", f.out); err == nil || strings.Contains(b, "public-test managed-ca") {
				t.Fatalf("unsealed material selected the managed CA recipe: %v %s", err, b)
			}
			if err := os.WriteFile(path, raw, 0644); err != nil {
				t.Fatal(err)
			}
		})
	}
	assertBase(t, f.out, before)
}
