package main

import (
	"fmt"
	"io"
	"math"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"

	"github.com/hansjone/auriga/deploy/bootstrap/materials"
	networkdeploy "github.com/hansjone/auriga/deploy/bootstrap/network"
	"github.com/hansjone/auriga/infra/gen/deployment"
	"github.com/hansjone/auriga/infra/gen/deploymentconfig"
)

// Keep the Docker MiB-to-byte conversion within signed int64, on every platform.
func parseInstanceMaxMemoryMB(value string) (int64, error) {
	if value == "" || value[0] < '1' || value[0] > '9' {
		return 0, fmt.Errorf("instance-max-memory-mb must be a canonical positive decimal MiB integer")
	}
	for i := 1; i < len(value); i++ {
		if value[i] < '0' || value[i] > '9' {
			return 0, fmt.Errorf("instance-max-memory-mb must be a canonical positive decimal MiB integer")
		}
	}
	memory, err := strconv.ParseInt(value, 10, 64)
	if err != nil || memory > math.MaxInt64/(1024*1024) {
		return 0, fmt.Errorf("instance-max-memory-mb exceeds the safe MiB-to-byte limit")
	}
	return memory, nil
}

func loadNASAccess(path, peerIP, subnet string, registryPort int) ([]byte, deploymentconfig.Config, error) {
	if path == "" {
		return nil, deploymentconfig.Config{}, fmt.Errorf("-access-file is required; migrate legacy Host/issuer variables to access JSON")
	}
	f := os.Stdin
	var err error
	if path != "-" {
		f, err = os.Open(path)
	}
	if err != nil {
		return nil, deploymentconfig.Config{}, fmt.Errorf("open access file")
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, (1<<20)+1))
	if err != nil {
		return nil, deploymentconfig.Config{}, fmt.Errorf("read access file")
	}
	cfg, err := deploymentconfig.Load(raw)
	if err != nil {
		return nil, cfg, err
	}
	if cfg.InstanceControl().EdgeContainer != "auriga-nas-test-edge" {
		return nil, cfg, fmt.Errorf("NAS edgeContainer must match fixed auriga-nas-test-edge")
	}
	ls := cfg.Listeners()
	for _, l := range []deployment.Listener{ls.Portal, ls.Admin, ls.InstanceControl} {
		if l.Address != "0.0.0.0" {
			return nil, cfg, fmt.Errorf("NAS container listeners require 0.0.0.0")
		}
	}
	if cfg.InstanceControl().Origin.Scheme != "http" || ls.InstanceControl.Tls {
		return nil, cfg, fmt.Errorf("NAS instance-control is an isolated HTTP listener")
	}
	if cfg.IngressKind() == "direct" {
		if cfg.PortalOrigin().Scheme != cfg.AdminOrigin().Scheme {
			return nil, cfg, fmt.Errorf("direct NAS browser listeners require matching schemes")
		}
	} else if cfg.PortalOrigin().Scheme != "https" || cfg.AdminOrigin().Scheme != "https" || !ls.Portal.Tls || !ls.Admin.Tls {
		return nil, cfg, fmt.Errorf("NAS trusted_proxy requires external HTTPS and two TLS+mTLS listeners")
	}
	prefix, err := netip.ParsePrefix(subnet)
	peer, peerErr := netip.ParseAddr(peerIP)
	if err != nil || !prefix.Addr().Is4() || prefix.Masked() != prefix || prefix.Bits() < 8 || prefix.Bits() > 30 || peerErr != nil || !peer.Is4() || peer.String() != peerIP || !prefix.Contains(peer) || peer == prefix.Addr() || peer == prefix.Addr().Next() {
		return nil, cfg, fmt.Errorf("invalid fixed edge component-network IPv4/prefix")
	}
	last := prefix.Addr().As4()
	mask := uint32(1) << (32 - prefix.Bits())
	n := uint32(last[0])<<24 | uint32(last[1])<<16 | uint32(last[2])<<8 | uint32(last[3])
	b := peer.As4()
	pn := uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
	if pn == n+mask-1 {
		return nil, cfg, fmt.Errorf("edge address must not be subnet broadcast")
	}
	if registryPort < 1024 || registryPort > 65535 {
		return nil, cfg, fmt.Errorf("registry port out of range")
	}
	return raw, cfg, nil
}

func nasMaterialSet(https ...bool) materials.MaterialSet {
	set := materials.MaterialSet{PrivateFiles: []string{"edge.secret", "internal.properties"}, ManagedDirectories: []string{"pki", "templates", "runtime-keys", "runtime-proofs", "network", "network-platform", "browser-tls", "litellm"}, PublicFiles: []string{
		"pki/proxy-ca.crt", "pki/proxy-ca.key", "pki/proxy-edge.crt", "pki/proxy-edge.key", "pki/proxy-client.crt", "pki/proxy-client.key",
		"pki/ca.crt", "pki/ca.key", "pki/workspace.crt", "pki/workspace.key", "pki/edge.crt", "pki/edge.key", "pki/iam.crt", "pki/iam.key", "pki/edge-iam.crt", "pki/edge-iam.key",
		"identity-signing-keys.json", "identity-keys.json", "ca-sha256.txt", "access.json", "access.env", "edge.config.json", "platform-access.yaml", "iam-healthcheck.sh", "agent-policy.yaml",
		"pki/gateway.crt", "pki/gateway.key", "litellm-runtime.json", "litellm-public.env",
		"litellm/master", "litellm/salt", "litellm/team", "litellm/pg_password",
	}}
	if len(https) > 0 && https[0] {
		set.PublicFiles = append(set.PublicFiles, "browser-tls/browser.crt", "browser-tls/browser.key")
	}
	for _, name := range networkdeploy.Files {
		set.PublicFiles = append(set.PublicFiles, "network/"+name)
	}
	for _, name := range networkdeploy.CredentialFiles {
		set.PublicFiles = append(set.PublicFiles, "network-platform/"+name)
	}
	return set
}

func validateNASLAN(value, subnet, componentSubnet string) error {
	address, err := netip.ParseAddr(value)
	prefix, prefixErr := netip.ParsePrefix(subnet)
	component, componentErr := netip.ParsePrefix(componentSubnet)
	if err != nil || !address.Is4() || address.String() != value || !address.IsGlobalUnicast() || address.IsLoopback() || prefixErr != nil || !prefix.Addr().Is4() || prefix.Masked() != prefix || prefix.String() != subnet || prefix.Bits() < 8 || prefix.Bits() > 30 || !prefix.Contains(address) || componentErr != nil || prefix.Overlaps(component) {
		return fmt.Errorf("required LAN IPv4/prefix must be canonical, matching and disjoint from the component network")
	}
	first := prefix.Addr().As4()
	ip := address.As4()
	network := uint32(first[0])<<24 | uint32(first[1])<<16 | uint32(first[2])<<8 | uint32(first[3])
	selected := uint32(ip[0])<<24 | uint32(ip[1])<<16 | uint32(ip[2])<<8 | uint32(ip[3])
	if selected <= network+1 || selected == network+(uint32(1)<<(32-prefix.Bits()))-1 {
		return fmt.Errorf("LAN address must not be network, default gateway or broadcast")
	}
	return nil
}

func writeNASConfig(out string, raw []byte, cfg deploymentconfig.Config, peer string, registry int, lanIP string, maxMemoryMB int64) {
	writeFile(filepath.Join(out, "access.json"), raw, 0o644)
	ls := cfg.Listeners()
	writeFile(filepath.Join(out, "access.env"), []byte(fmt.Sprintf("AURIGA_NAS_ISSUER=%s\nAURIGA_NAS_PORTAL_HOST=%s\nAURIGA_NAS_ADMIN_HOST=%s\nAURIGA_NAS_PORTAL_URL=%s\nAURIGA_NAS_ADMIN_URL=%s\nAURIGA_NAS_PORTAL_PORT=%d\nAURIGA_NAS_ADMIN_PORT=%d\nAURIGA_NAS_PORTAL_TARGET_PORT=%d\nAURIGA_NAS_ADMIN_TARGET_PORT=%d\n", cfg.Issuer(), cfg.PortalOrigin().Host, cfg.AdminOrigin().Host, cfg.PortalURL(), cfg.AdminURL(), cfg.PortalOrigin().Port, cfg.AdminOrigin().Port, ls.Portal.Port, ls.Admin.Port)), 0o644)
	edgeConfig := map[string]any{
		"instanceControlIngress": map[string]any{"denyLocalIPv4": []string{lanIP}},
		"accessConfigFile":       "/etc/auriga/access.json", "allowLocalIngress": true,
		"upstreams":        map[string]string{"iam": "https://platform:8081", "org": "https://platform:8081", "workspace": "https://platform:8081", "network-submit": "https://network:18443", "ai-device": "https://ai-relay:18445"},
		"litellmOrigin":    "http://litellm:4000",
		"deviceHop":        map[string]string{"certFile": "/etc/auriga/pki/edge-iam.crt", "keyFile": "/etc/auriga/pki/edge-iam.key", "caFile": "/etc/auriga/pki/ca.crt", "serverName": "ai-relay"},
		"networkSubmitHop": map[string]string{"certFile": "/etc/auriga/pki/network-edge.pem", "keyFile": "/etc/auriga/pki/network-edge.key", "caFile": "/etc/auriga/pki/network-server-ca.pem", "serverName": "submit.network.internal"},
		"networkWss": map[string]any{
			"enabled": true, "path": "/net/v1/ws", "upstreamUrl": "wss://network-broker:18444",
			"caFile": "/etc/auriga/pki/network-server-ca.pem", "serverName": "broker.network.internal",
			"allowedOrigins": []string{cfg.PortalURL()},
		},
		"iamHop": map[string]string{"certFile": "/etc/auriga/pki/edge-iam.crt", "keyFile": "/etc/auriga/pki/edge-iam.key", "caFile": "/etc/auriga/pki/ca.crt", "serverName": "platform"},
		"iamRpc": map[string]any{"address": "platform:9091", "serverName": "platform", "timeoutMillis": 2000, "serviceTokenFile": "/run/auriga-internal/edge/service.jwt"},
		"static": map[string]string{"portalRoot": "/srv/auriga/static/portal", "adminRoot": "/srv/auriga/static/admin"},
		"tls":    map[string]string{"mode": "off"}, "rateLimit": map[string]any{"requestsPerSecond": 50, "burst": 100}, "identityKeysFile": "/etc/auriga/identity-signing-keys.json", "sessionStore": "nats",
		"nodeTLS": map[string]string{"certFile": "/etc/auriga/pki/edge.crt", "keyFile": "/etc/auriga/pki/edge.key", "caFile": "/etc/auriga/pki/ca.crt"},
	}
	if cfg.IngressKind() == "trusted_proxy" {
		edgeConfig["tls"] = map[string]string{"mode": "files", "certFile": "/etc/auriga/pki/proxy-edge.crt", "keyFile": "/etc/auriga/pki/proxy-edge.key"}
		edgeConfig["proxyTLS"] = map[string]string{"caFile": "/etc/auriga/pki/proxy-ca.crt", "identityUri": "auriga://proxy/nas-test"}
	}
	if cfg.IngressKind() == "direct" && cfg.PortalOrigin().Scheme == "https" {
		edgeConfig["tls"] = map[string]string{"mode": "files", "certFile": "/etc/auriga/pki/browser.crt", "keyFile": "/etc/auriga/pki/browser.key"}
	}
	writeJSON(filepath.Join(out, "edge.config.json"), edgeConfig, 0o644)
	writeFile(filepath.Join(out, "platform-access.yaml"), []byte(fmt.Sprintf(`auriga:
  platform:
    team:
      rp-id: "%s"
      origin: "%s"
  iam:
    issuer: "%s"
    edge-hop:
      peer-addresses: ["%s", "127.0.0.1"]
      allow-loopback-http: false
  security:
    jwt:
      issuer: "%s"
`, cfg.PortalOrigin().Host, cfg.PortalURL(), cfg.Issuer(), peer, cfg.Issuer())), 0o644)
	writeFile(filepath.Join(out, "iam-healthcheck.sh"), []byte(`#!/bin/sh
set -eu
test -s /run/auriga-internal/edge/service.jwt
exec curl --fail --silent --show-error --noproxy '*' --cacert /etc/auriga/pki/ca.crt --cert /etc/auriga/pki/edge-iam.crt --key /etc/auriga/pki/edge-iam.key --resolve platform:8081:127.0.0.1 https://platform:8081/actuator/health
`), 0o644)
	writeFile(filepath.Join(out, "agent-policy.yaml"), []byte(fmt.Sprintf("allowedImageRepos:\n  - 127.0.0.1:%d/auriga/\n  - localhost:%d/auriga/\nmaxCPUMillis: 500\nmaxMemoryMB: %d\nmaxPIDs: 128\nmaxArtifactBytes: 2147483648\n", registry, registry, maxMemoryMB)), 0o644)
}
