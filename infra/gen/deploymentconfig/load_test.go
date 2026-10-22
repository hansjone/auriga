package deploymentconfig

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hansjone/auriga/infra/gen/deployment"
)

const fixtures = "../../../contracts/deployment/fixtures"

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(fixtures, name))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

type patch struct {
	Pointer string `json:"pointer"`
	Value   any    `json:"value"`
	Remove  bool   `json:"remove"`
}
type negativeCase struct {
	Name string `json:"name"`
	Base string `json:"base"`
	patch
	AdditionalChanges []patch `json:"additionalChanges"`
}

func changed(t *testing.T, base string, changes ...patch) []byte {
	t.Helper()
	var value map[string]any
	if err := json.Unmarshal(fixture(t, base), &value); err != nil {
		t.Fatal(err)
	}
	for _, change := range changes {
		parts := strings.Split(change.Pointer, ".")
		target := value
		for _, part := range parts[:len(parts)-1] {
			target = target[part].(map[string]any)
		}
		if change.Remove {
			delete(target, parts[len(parts)-1])
		} else {
			target[parts[len(parts)-1]] = change.Value
		}
	}
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func rejected(t *testing.T, data []byte, field string) {
	t.Helper()
	config, err := Load(data)
	var failure *ValidationError
	if !errors.As(err, &failure) || failure.Code() != "common.validation_failed" || config.Valid() {
		t.Fatalf("expected closed validation failure, got config=%v error=%v", config.Valid(), err)
	}
	if field != "" && failure.Field != field {
		t.Fatalf("field=%q, want %q (%v)", failure.Field, field, err)
	}
}

func TestOriginalPositiveFixtures(t *testing.T) {
	for _, name := range []string{"ip-http.json", "domain-proxy.json", "domain-root-portal.json", "domain-nested-parent.json"} {
		t.Run(name, func(t *testing.T) {
			config, err := Load(fixture(t, name))
			if err != nil {
				t.Fatal(err)
			}
			if !config.Valid() || config.TenantID() != "019a0000-0000-7000-8000-000000000001" || config.Issuer() != config.PortalURL() {
				t.Fatal("lost fixed identity")
			}
		})
	}
}

func TestOriginalNegativeFixtures(t *testing.T) {
	for _, file := range []struct {
		name  string
		count int
	}{{"schema-negative.json", 32}, {"startup-negative.json", 12}} {
		var cases []negativeCase
		if err := json.Unmarshal(fixture(t, file.name), &cases); err != nil {
			t.Fatal(err)
		}
		if len(cases) != file.count {
			t.Fatalf("unexpected fixture count %d", len(cases))
		}
		for _, c := range cases {
			t.Run(file.name+"/"+c.Name, func(t *testing.T) {
				data := changed(t, c.Base, append([]patch{c.patch}, c.AdditionalChanges...)...)
				field := ""
				if file.name == "startup-negative.json" {
					var value any
					if err := json.Unmarshal(data, &value); err != nil {
						t.Fatal(err)
					}
					schema, err := compiledSchema()
					if err != nil {
						t.Fatal(err)
					}
					if err := schema.Validate(value); err != nil {
						t.Fatalf("startup negative must first pass full structure: %v", err)
					}
					field = map[string]string{
						"issuer-differs": "/issuerOrigin", "same-origin-admin": "/adminOrigin", "different-ip-admin": "/adminOrigin",
						"http-unacknowledged": "/insecureHttpAcknowledged", "listener-collision": "/listeners/admin", "direct-tls-mismatch": "/listeners",
						"alias-origin-mismatch": "/instanceControl/origin/host", "internal-port-mismatch": "/instanceControl/origin",
						"portal-captured-by-instance-host": "/portalOrigin/host", "admin-captured-by-instance-host": "/adminOrigin/host",
						"proxy-cidr-invalid": "/ingress/peerCidrs/0", "proxy-cidr-all": "/ingress/peerCidrs/0",
					}[c.Name]
					if field == "" {
						t.Fatal("startup failure requires a specific semantic field")
					}
				}
				rejected(t, data, field)
			})
		}
	}
}

func TestOriginalSchemaIsEmbeddedUnchanged(t *testing.T) {
	data, err := os.ReadFile("../../../contracts/deployment/access.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	if strings.ReplaceAll(string(data), "\r\n", "\n") != deployment.AccessSchemaJSON() {
		t.Fatal("embedded schema drift")
	}
}

func TestRequiredAndNullFields(t *testing.T) {
	var schema struct {
		Required []string `json:"required"`
	}
	if err := json.Unmarshal([]byte(deployment.AccessSchemaJSON()), &schema); err != nil {
		t.Fatal(err)
	}
	for _, field := range append(schema.Required, "portalOrigin.scheme", "portalOrigin.host", "portalOrigin.port", "listeners.portal.tls", "listeners.portal.address", "listeners.portal.port", "instances.trustedTeamAcknowledged", "instances.prefix", "ingress.kind", "instanceControl.dnsAlias", "instanceControl.origin", "instanceControl.edgeContainer") {
		for _, remove := range []bool{false, true} {
			t.Run(field+"/"+map[bool]string{false: "null", true: "missing"}[remove], func(t *testing.T) { rejected(t, changed(t, "ip-http.json", patch{Pointer: field, Remove: remove}), "") })
		}
	}
	rejected(t, changed(t, "ip-http.json", patch{"tenantId", nil, true}), "/tenantId")
	rejected(t, changed(t, "ip-http.json", patch{"listeners.portal.extra", true, false}), "/listeners/portal/extra")
}

func TestAdditionalSemantics(t *testing.T) {
	cases := []struct {
		name, base string
		changes    []patch
		field      string
	}{
		{"ip-scheme-mismatch", "ip-http.json", []patch{{"adminOrigin.scheme", "https", false}}, "/adminOrigin"},
		{"domain-same-host-different-port", "domain-proxy.json", []patch{{"adminOrigin.host", "portal.example.com", false}, {"adminOrigin.port", 8443, false}}, "/adminOrigin/host"},
		{"fixed-parent-root", "domain-proxy.json", []patch{{"adminOrigin.host", "i.example.com", false}}, "/adminOrigin/host"},
		{"fixed-nonuuid-instance-label", "domain-proxy.json", []patch{{"adminOrigin.host", "not-a-uuid.i.example.com", false}}, "/adminOrigin/host"},
		{"instances-scheme", "domain-proxy.json", []patch{{"instances.scheme", "http", false}}, "/instances"},
		{"instances-port", "domain-proxy.json", []patch{{"instances.port", 8443, false}}, "/instances"},
		{"wildcard-vs-concrete", "ip-http.json", []patch{{"listeners.admin.address", "127.0.0.1", false}, {"listeners.admin.port", 8080, false}}, "/listeners/admin"},
		{"concrete-vs-wildcard", "ip-http.json", []patch{{"listeners.portal.address", "127.0.0.1", false}, {"listeners.admin.port", 8080, false}}, "/listeners/admin"},
		{"control-vs-admin", "ip-http.json", []patch{{"listeners.instanceControl.port", 8082, false}}, "/listeners/instanceControl"},
		{"proxy-no-tls", "domain-proxy.json", []patch{{"listeners.admin.tls", false, false}}, "/listeners"},
		{"proxy-host-bits", "domain-proxy.json", []patch{{"ingress.peerCidrs", []string{"192.0.2.1/24"}, false}}, "/ingress/peerCidrs/0"},
		{"proxy-ipv6-zero", "domain-proxy.json", []patch{{"ingress.peerCidrs", []string{"::/0"}, false}}, "/ingress/peerCidrs/0"},
		{"proxy-ipv6-noncanonical", "domain-proxy.json", []patch{{"ingress.peerCidrs", []string{"2001:0db8::/32"}, false}}, "/ingress/peerCidrs/0"},
		{"proxy-mapped-ipv4", "domain-proxy.json", []patch{{"ingress.peerCidrs", []string{"::ffff:192.0.2.0/120"}, false}}, "/ingress/peerCidrs/0"},
		{"internal-tls", "ip-http.json", []patch{{"instanceControl.origin.scheme", "https", false}}, "/instanceControl/origin"},
		{"alias-browser", "domain-proxy.json", []patch{{"instanceControl.dnsAlias", "admin.example.com", false}, {"instanceControl.origin.host", "admin.example.com", false}}, "/instanceControl/dnsAlias"},
		{"alias-instance-root", "domain-proxy.json", []patch{{"instanceControl.dnsAlias", "i.example.com", false}, {"instanceControl.origin.host", "i.example.com", false}}, "/instanceControl/dnsAlias"},
		{"alias-instance-child", "domain-proxy.json", []patch{{"instanceControl.dnsAlias", "control.i.example.com", false}, {"instanceControl.origin.host", "control.i.example.com", false}}, "/instanceControl/dnsAlias"},
		{"invalid-alabel", "domain-proxy.json", []patch{{"adminOrigin.host", "xn--a.example.com", false}}, "/adminOrigin/host"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) { rejected(t, changed(t, c.base, c.changes...), c.field) })
	}
}

func TestStrictJSONAndUnionStructure(t *testing.T) {
	base := string(fixture(t, "ip-http.json"))
	for _, input := range []string{
		base + " {}", base + " null", base + " garbage", base[:len(base)-2], "null", "[]", "",
		strings.Replace(base, `"schemaVersion": 1`, `"schemaVersion": 1, "schemaVersion": 1`, 1),
		strings.Replace(base, `"scheme": "http"`, `"scheme": "http", "sc\u0068eme": "http"`, 1),
		strings.Replace(base, `"port": 8080`, `"port": 8080, "port": 8082`, 1),
		strings.Replace(base, `"kind": "direct"`, `"kind": "direct", "kind": "trusted_proxy"`, 1),
		strings.Replace(base, `"port": 8080`, `"port": 8080.0000000000000000000001`, 1),
		strings.Repeat("[", 66) + strings.Repeat("]", 66),
	} {
		rejected(t, []byte(input), "")
	}
	rejected(t, []byte{'{', '"', 0xff, '"', ':', '1', '}'}, "")
	for _, p := range []patch{
		{"schemaVersion", true, false}, {"schemaVersion", "1", false}, {"schemaVersion", 1.5, false},
		{"instances.trustedTeamAcknowledged", 1, false},
		{"listeners.portal.extra", true, false}, {"portalOrigin.Scheme", "http", false},
		{"ingress.peerCidrs", []string{"127.0.0.1/32"}, false}, {"instances.parentDomain", "i.example.com", false},
		{"portalOrigin.port", true, false}, {"portalOrigin.port", 1e99, false},
		{"instances", map[string]any{"mode": "path", "prefix": "/i/", "trustedTeamAcknowledged": true, "scheme": "http", "parentDomain": "i.example.com", "port": 80}, false},
	} {
		rejected(t, changed(t, "ip-http.json", p), "")
	}
	for _, host := range []string{"192.0.2.10.", "192.0.2.010", "3221225994", "0xc000020a", "192.0.2.10%2f", "192.0.2.10\\", "::ffff:192.0.2.10"} {
		rejected(t, changed(t, "ip-http.json", patch{"portalOrigin.host", host, false}), "")
	}
	for _, p := range []patch{
		{"ingress.peerCidrs", nil, false}, {"ingress.peerCidrs", []string{"127.0.0.1/32", "127.0.0.1/32"}, false},
		{"ingress.requireMtls", nil, false}, {"ingress.preserveAuthority", nil, false}, {"instances.scheme", nil, false}, {"instances.port", nil, false},
	} {
		rejected(t, changed(t, "domain-proxy.json", p), "")
	}
}

func TestHTTPSDirectAndHTTPProxy(t *testing.T) {
	config, err := Load(changed(t, "ip-http.json",
		patch{"portalOrigin.scheme", "https", false}, patch{"issuerOrigin.scheme", "https", false}, patch{"adminOrigin.scheme", "https", false},
		patch{"portalOrigin.port", 443, false}, patch{"issuerOrigin.port", 443, false}, patch{"adminOrigin.port", 8443, false},
		patch{"listeners.portal.tls", true, false}, patch{"listeners.admin.tls", true, false}, patch{"insecureHttpAcknowledged", false, false}))
	if err != nil || config.Issuer() != "https://192.0.2.10" || config.AdminURL() != "https://192.0.2.10:8443" {
		t.Fatalf("direct HTTPS and default ports: %v", err)
	}
	config, err = Load(changed(t, "domain-proxy.json",
		patch{"portalOrigin.scheme", "http", false}, patch{"issuerOrigin.scheme", "http", false}, patch{"adminOrigin.scheme", "http", false}, patch{"instances.scheme", "http", false},
		patch{"portalOrigin.port", 80, false}, patch{"issuerOrigin.port", 80, false}, patch{"adminOrigin.port", 80, false}, patch{"instances.port", 80, false}, patch{"insecureHttpAcknowledged", true, false}))
	if err != nil || config.PortalURL() != "http://portal.example.com" || config.IngressKind() != "trusted_proxy" {
		t.Fatalf("proxy TLS must be independent of explicitly acknowledged browser HTTP: %v", err)
	}
}

func TestNormalizationAndCopies(t *testing.T) {
	data := changed(t, "domain-proxy.json", patch{"ingress.peerCidrs", []string{"192.0.2.0/24", "2001:db8::/32"}, false})
	config, err := Load(data)
	if err != nil {
		t.Fatal(err)
	}
	if config.Issuer() != "https://portal.example.com" || config.AdminURL() != "https://admin.example.com" {
		t.Fatal("default port serialization")
	}
	peers := config.ProxyPeerCIDRs()
	peers[0] = peers[1]
	if config.ProxyPeerCIDRs()[0].String() != "192.0.2.0/24" {
		t.Fatal("mutable CIDR escape")
	}
	portal := config.PortalOrigin()
	portal.Host = "attacker.example.com"
	listeners := config.Listeners()
	listeners.Portal.Port = 1
	control := config.InstanceControl()
	control.Origin.Host = "attacker.internal"
	if config.PortalOrigin().Host != "portal.example.com" || config.Listeners().Portal.Port != 18080 || config.InstanceControl().Origin.Host != "auriga-instance.internal" {
		t.Fatal("mutable generated model escape")
	}
	if config.IngressKind() != "trusted_proxy" || config.InstanceParentDomain() != "i.example.com" {
		t.Fatal("lost branch")
	}
	ip := string(fixture(t, "ip-http.json"))
	ip = strings.ReplaceAll(ip, `"port": 8080`, `"port": 8.080e3`)
	ipConfig, err := Load([]byte(ip))
	if err != nil || ipConfig.PortalURL() != "http://192.0.2.10:8080" || ipConfig.IngressKind() != "direct" {
		t.Fatalf("numeric normalization: %v", err)
	}
	// Different concrete IPv4 sockets may share a port; NAT also allows local
	// and external ports to differ. No listener is actually opened in tests.
	_, err = Load(changed(t, "ip-http.json", patch{"listeners.portal.address", "192.0.2.10", false}, patch{"listeners.admin.address", "192.0.2.11", false}, patch{"listeners.admin.port", 8080, false}))
	if err != nil {
		t.Fatal(err)
	}
	if (Config{}).Valid() || (Config{}).IngressKind() != "" {
		t.Fatal("zero config accepted")
	}
}

func TestInstanceSubdomainIsValidatedValueCopy(t *testing.T) {
	for _, port := range []int{443, 8443} {
		c, err := Load(changed(t, "domain-proxy.json", patch{Pointer: "instances.port", Value: port}, patch{Pointer: "portalOrigin.port", Value: port}, patch{Pointer: "issuerOrigin.port", Value: port}))
		if err != nil {
			t.Fatal(err)
		}
		got, ok := c.InstanceSubdomain()
		if !ok || got.Port != int32(port) || got.Scheme != "https" || got.ParentDomain != "i.example.com" {
			t.Fatalf("subdomain: %+v %t", got, ok)
		}
		got.Port = 1234
		got.ParentDomain = "evil.example.com"
		again, _ := c.InstanceSubdomain()
		if again.Port != int32(port) || again.ParentDomain != "i.example.com" {
			t.Fatal("accessor exposed mutable state")
		}
	}
	c, err := Load(fixture(t, "ip-http.json"))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := c.InstanceSubdomain(); ok {
		t.Fatal("IP path has no subdomain")
	}
	if _, ok := (Config{}).InstanceSubdomain(); ok {
		t.Fatal("invalid Config has no subdomain")
	}
}
