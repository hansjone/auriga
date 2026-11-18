package main

import (
	"bytes"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"time"
	"unicode"

	"github.com/hansjone/auriga/infra/gen/deploymentconfig"
)

const (
	managedCAFile   = "managed-ca.pem"
	managedCARecipe = "managed-ca.yml"
	managedCALimit  = 64 << 10 // Workspace and the agent renderer share this limit.
)

func readManagedCA(path string, access deploymentconfig.Config, browserChain []byte) ([]byte, error) {
	if path == "" {
		return nil, nil
	}
	if err := validateManagedCAAccess(access); err != nil {
		return nil, err
	}
	invalid := fmt.Errorf("invalid public managed CA bundle")
	raw, err := collabRead(path, managedCALimit, false)
	if err != nil {
		return nil, invalid
	}
	now := time.Now()
	root, bundle, err := parseManagedCAs(raw, now)
	if err != nil {
		return nil, err
	}

	// ReadBrowserTLS has already checked the key, SANs and every chain member.
	block, rest := pem.Decode(browserChain)
	if block == nil {
		return nil, invalid
	}
	leaf, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, invalid
	}
	untrusted := fmt.Errorf("browser HTTPS chain does not verify with the managed CA bundle")
	for _, host := range []string{access.PortalOrigin().Host, access.AdminOrigin().Host, "instance." + access.InstanceParentDomain()} {
		if err := leaf.VerifyHostname(host); err != nil {
			return nil, untrusted
		}
	}
	roots := x509.NewCertPool()
	roots.AddCert(root)
	chain := x509.NewCertPool()
	chain.AppendCertsFromPEM(rest)
	verified, err := leaf.Verify(x509.VerifyOptions{Roots: roots, Intermediates: chain, CurrentTime: now})
	if err != nil {
		return nil, untrusted
	}
	for _, path := range verified {
		// All bundle members must belong to the same verified path. Keep the
		// intermediates in the supplied server chain out of the root pool.
		matched := 0
		for _, ca := range path[1:] {
			if _, ok := bundle[string(ca.Raw)]; ok {
				matched++
			}
		}
		if matched == len(bundle) {
			return raw, nil
		}
	}
	return nil, fmt.Errorf("managed CA bundle contains a certificate outside the verified browser HTTPS chain")
}

func parseManagedCAs(raw []byte, now time.Time) (*x509.Certificate, map[string]struct{}, error) {
	const begin, end = "-----BEGIN CERTIFICATE-----", "-----END CERTIFICATE-----"
	invalid := fmt.Errorf("invalid public managed CA bundle")
	bundle := make(map[string]struct{})
	var root *x509.Certificate
	for rest := raw; len(bytes.TrimSpace(rest)) > 0; {
		body, ok := bytes.CutPrefix(bytes.TrimLeftFunc(rest, unicode.IsSpace), []byte(begin))
		encoded, endLine, closed := bytes.Cut(body, []byte(end))
		if !ok || !closed || bytes.ContainsRune(encoded, '-') {
			return nil, nil, invalid
		}
		// Decode the original bytes through the complete END line. This keeps
		// the renderer's line boundaries and prevents skipping a malformed block.
		_, tail, _ := bytes.Cut(endLine, []byte("\n"))
		block, trailing := pem.Decode(rest[:len(rest)-len(tail)])
		if block == nil || block.Type != "CERTIFICATE" || len(block.Headers) != 0 || len(trailing) != 0 {
			return nil, nil, invalid
		}
		ca, err := x509.ParseCertificate(block.Bytes)
		if err != nil || !ca.IsCA || !ca.BasicConstraintsValid || now.Before(ca.NotBefore) || !now.Before(ca.NotAfter) || (ca.KeyUsage != 0 && ca.KeyUsage&x509.KeyUsageCertSign == 0) {
			return nil, nil, invalid
		}
		der := string(ca.Raw)
		if _, exists := bundle[der]; exists {
			return nil, nil, fmt.Errorf("duplicate certificate in managed CA bundle")
		}
		bundle[der] = struct{}{}
		// Node's default TLS verification requires a complete chain to a
		// self-signed root. Go would also accept an intermediate as an anchor.
		if bytes.Equal(ca.RawIssuer, ca.RawSubject) && ca.CheckSignatureFrom(ca) == nil {
			if root != nil {
				return nil, nil, fmt.Errorf("managed CA bundle must contain exactly one self-signed root")
			}
			root = ca
		}
		rest = tail
	}
	if root == nil {
		return nil, nil, fmt.Errorf("public managed CA bundle requires a self-signed root")
	}
	return root, bundle, nil
}

func validateManagedCAAccess(access deploymentconfig.Config) error {
	portal := access.PortalOrigin()
	if portal.Scheme != "https" || access.Mode() != "domain" {
		return fmt.Errorf("managed CA requires direct HTTPS domain access")
	}
	// Instances dial the edge DNS alias directly, without the host port publish.
	if access.Listeners().Portal.Port != portal.Port {
		return fmt.Errorf("managed CA requires matching published and edge portal ports")
	}
	return nil
}

// Compose loads this fixed recipe only from a verified base receipt. The
// public trust file is shared read-only; the leaf key remains edge-only.
func managedCACompose(access deploymentconfig.Config) []byte {
	portalLoopback := access.PortalOrigin().Host + ":127.0.0.1"
	return fmt.Appendf(nil, `services:
  platform:
    extra_hosts:
      - %q
    environment:
      AURIGA_LINKS_ALLOWED_ORIGINS: "%s${AURIGA_LINKS_ALLOWED_ORIGINS:+,}${AURIGA_LINKS_ALLOWED_ORIGINS:-}"
      AURIGA_WORKSPACE_MANAGED_PLATFORM_ORIGIN: %q
      AURIGA_WORKSPACE_MANAGED_CA_FILE: /etc/auriga/managed-ca.pem
    volumes:
      - &managed-ca
        type: bind
        source: ./var/managed-ca.pem
        target: /etc/auriga/managed-ca.pem
        read_only: true
        bind:
          create_host_path: false
  agent:
    environment:
      AURIGA_AGENT_MANAGED_CA_FILE: /etc/auriga/managed-ca.pem
    volumes:
      - *managed-ca
  collab:
    extra_hosts:
      - %q
`, portalLoopback, access.PortalURL(), access.PortalURL(), portalLoopback)
}

func hasManagedCA(out string) (bool, error) {
	ca, caErr := os.Lstat(filepath.Join(out, managedCAFile))
	recipe, recipeErr := os.Lstat(filepath.Join(out, managedCARecipe))
	if os.IsNotExist(caErr) && os.IsNotExist(recipeErr) {
		return false, nil
	}
	if caErr != nil || recipeErr != nil || !ca.Mode().IsRegular() || !recipe.Mode().IsRegular() {
		return false, fmt.Errorf("invalid managed CA installation files")
	}
	return true, nil
}

func writeManagedCA(out string, ca []byte, access deploymentconfig.Config) error {
	for _, file := range []struct {
		name string
		raw  []byte
	}{{managedCAFile, ca}, {managedCARecipe, managedCACompose(access)}} {
		f, err := os.OpenFile(filepath.Join(out, file.name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err != nil {
			return fmt.Errorf("create managed CA installation file: %w", err)
		}
		writeErr := f.Chmod(0o644)
		if writeErr == nil {
			_, writeErr = f.Write(file.raw)
		}
		closeErr := f.Close()
		if writeErr != nil || closeErr != nil {
			return fmt.Errorf("write managed CA installation file")
		}
	}
	return nil
}
