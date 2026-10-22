package main

import (
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hansjone/auriga/infra/gen/deployment"
	"github.com/hansjone/auriga/infra/gen/deploymentconfig"
)

func assertAccessMaterials(t *testing.T, out string, observer bool) {
	t.Helper()
	read := func(name string) []byte {
		t.Helper()
		raw, err := os.ReadFile(filepath.Join(out, name))
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	access, err := deploymentconfig.Load(read("access.json"))
	if err != nil {
		t.Fatal(err)
	}
	if access.TenantID() != composeTenant || access.Issuer() != "http://portal.example.com:8080" ||
		access.AdminURL() != "http://admin.example.com:8084" || access.InstanceControl().Origin.Port != controlPort {
		t.Fatal("generated access differs from fixed seed and external/internal origins")
	}
	var edge struct {
		AccessConfigFile string            `json:"accessConfigFile"`
		Upstreams        map[string]string `json:"upstreams"`
		IAMHop           map[string]string `json:"iamHop"`
		IAMRPC           struct {
			Address, ServerName, ServiceTokenFile string
			TimeoutMillis                         int
		} `json:"iamRpc"`
		NodeTLS map[string]string `json:"nodeTLS"`
	}
	if err := json.Unmarshal(read("edge.config.json"), &edge); err != nil {
		t.Fatal(err)
	}
	if edge.AccessConfigFile != "/etc/auriga/access.json" {
		t.Fatal("edge must load shared access.json")
	}
	for _, service := range []string{"iam", "org", "workspace"} {
		port := iamPort
		if observer {
			port = observerPort
		}
		if edge.Upstreams[service] != fmt.Sprintf("https://host.docker.internal:%d", port) {
			t.Fatalf("%s is not the authenticated combined origin", service)
		}
	}
	var observation struct {
		Enabled                             bool
		ListenPort                          int
		Upstream, ServerName, ClientDNSName string
	}
	if err := json.Unmarshal(read("heartbeat-observer.json"), &observation); err != nil {
		t.Fatal(err)
	}
	if observation.Enabled != observer || observation.ListenPort != observerPort || observation.Upstream != "https://127.0.0.1:8081" ||
		observation.ServerName != iamServerName || observation.ClientDNSName != edgeHopDNSName {
		t.Fatal("observer installation differs from authenticated combined origin")
	}
	if edge.IAMHop["serverName"] != iamServerName || edge.IAMHop["certFile"] == edge.NodeTLS["certFile"] {
		t.Fatal("IAM HTTP hop must have its own fixed DNS identity")
	}
	if edge.IAMRPC.Address != fmt.Sprintf("host.docker.internal:%d", iamRPCPort) || edge.IAMRPC.ServerName != iamServerName || edge.IAMRPC.ServiceTokenFile != "/run/auriga-internal/edge/service.jwt" || edge.IAMRPC.TimeoutMillis != 2000 {
		t.Fatal("internal RPC must consume the fixed directory-mounted issuer export")
	}
	cert := func(name string) *x509.Certificate {
		t.Helper()
		block, _ := pem.Decode(read("pki/" + name + ".crt"))
		if block == nil {
			t.Fatalf("no PEM certificate: %s", name)
		}
		parsed, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			t.Fatal(err)
		}
		return parsed
	}
	ca := cert("ca")
	roots := x509.NewCertPool()
	roots.AddCert(ca)
	for _, tc := range []struct {
		name, dns string
		eku       x509.ExtKeyUsage
	}{
		{"iam", iamServerName, x509.ExtKeyUsageServerAuth},
		{"edge-iam", edgeHopDNSName, x509.ExtKeyUsageClientAuth},
	} {
		leaf := cert(tc.name)
		if len(leaf.DNSNames) != 1 || leaf.DNSNames[0] != tc.dns || len(leaf.ExtKeyUsage) != 1 || leaf.ExtKeyUsage[0] != tc.eku {
			t.Fatalf("%s has unexpected SAN/EKU", tc.name)
		}
		if _, err := leaf.Verify(x509.VerifyOptions{Roots: roots, DNSName: tc.dns, KeyUsages: []x509.ExtKeyUsage{tc.eku}}); err != nil {
			t.Fatal(err)
		}
		other := x509.ExtKeyUsageServerAuth
		if tc.eku == other {
			other = x509.ExtKeyUsageClientAuth
		}
		if _, err := leaf.Verify(x509.VerifyOptions{Roots: roots, KeyUsages: []x509.ExtKeyUsage{other}}); err == nil {
			t.Fatalf("%s permits the wrong TLS role", tc.name)
		}
	}
	if len(cert("edge").DNSNames) != 0 || cert("edge").URIs[0].String() != "auriga://edge/compose-e2e" ||
		cert("workspace").URIs[0].String() != "auriga://workspace" {
		t.Fatal("original node PKI identity changed")
	}
	var client struct{ BaseURL, ServerName, CAFile, CertFile, KeyFile string }
	if err := json.Unmarshal(read("iam-client.json"), &client); err != nil {
		t.Fatal(err)
	}
	if client.BaseURL != "https://127.0.0.1:8081" || client.ServerName != iamServerName ||
		client.CertFile != "pki/edge-iam.crt" || client.KeyFile != "pki/edge-iam.key" || client.CAFile != "pki/ca.crt" {
		t.Fatal("e2e TLS client differs from installation")
	}
	if !strings.Contains(string(read("iam-healthcheck.sh")), "https://platform:8081/actuator/health") ||
		!strings.Contains(string(read("access.env")), "AURIGA_IAM_ISSUER='"+access.Issuer()+"'") {
		t.Fatal("health/issuer materials differ from access")
	}
}

func TestAccessRejectsConflictingInstallation(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*deployment.Access)
	}{
		{"shared portal/admin socket", func(a *deployment.Access) { a.Listeners.Admin.Port = a.Listeners.Portal.Port }},
		{"shared control/portal socket", func(a *deployment.Access) {
			a.Listeners.InstanceControl.Port = a.Listeners.Portal.Port
			a.InstanceControl.Origin.Port = a.Listeners.Portal.Port
		}},
		{"issuer drift", func(a *deployment.Access) { a.IssuerOrigin.Port = adminPort }},
		{"missing HTTP acknowledgement", func(a *deployment.Access) { a.InsecureHttpAcknowledged = false }},
		{"browser alias collision", func(a *deployment.Access) {
			a.InstanceControl.DnsAlias = portalHost
			a.InstanceControl.Origin.Host = portalHost
		}},
		{"wrong internal port", func(a *deployment.Access) { a.InstanceControl.Origin.Port = portalPort }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := composeAccess("auriga-m2-edge")
			tc.mutate(&a)
			if _, _, err := marshalAccess(a); err == nil {
				t.Fatal("invalid public installation accepted")
			}
		})
	}
}
