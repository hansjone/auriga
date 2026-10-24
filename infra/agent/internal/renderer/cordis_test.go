package renderer_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hansjone/auriga/infra/agent/internal/identity"
	"github.com/hansjone/auriga/infra/agent/internal/renderer"
	nodev1 "github.com/hansjone/auriga/infra/gen/auriga/node/v1"
	"github.com/hansjone/auriga/infra/gen/deploymentconfig"
	"gopkg.in/yaml.v3"
)

func renderCordis(t *testing.T, spec *nodev1.InstanceSpec, parent string) (string, error) {
	t.Helper()
	priv, pub, err := identity.GenerateSealingKey()
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := renderer.SealForTest(pub, spec.InstanceId, "instance_token", spec.Generation, []byte("tok-secret"))
	if err != nil {
		t.Fatal(err)
	}
	if spec.Secrets == nil {
		spec.Secrets = &nodev1.Secrets{}
	}
	spec.Secrets.InstanceToken = sealed
	root := t.TempDir()
	opt := renderer.Options{
		MetaDir:        filepath.Join(root, "meta"),
		HomeDir:        filepath.Join(root, "home"),
		Access:         testAccess(t, parent),
		SealingPrivate: priv,
	}
	_ = os.MkdirAll(opt.HomeDir, 0o700)
	res, err := renderer.Render(spec, opt)
	if err != nil {
		return "", err
	}
	raw, err := os.ReadFile(res.CordisPatchFile)
	if err != nil {
		t.Fatal(err)
	}
	env, _ := os.ReadFile(res.EnvFile)
	if !strings.Contains(string(env), "AURIGA_PLATFORM_URL=http://auriga-instance.internal:8081\n") {
		t.Fatalf("platform url not rendered: %s", env)
	}
	return string(raw), nil
}

func baseSpec() *nodev1.InstanceSpec {
	return &nodev1.InstanceSpec{
		InstanceId: "inst-c1", TenantId: "t1", OwnerId: "u1", Generation: 3,
		Runtime: nodev1.RuntimeKind_RUNTIME_KIND_DOCKER,
		Profile: &nodev1.Profile{ListenPort: 7861},
	}
}

func TestCordisPatchIsLoaderPatchList(t *testing.T) {
	spec := baseSpec()
	spec.Profile.Patches = []*nodev1.ConfigPatch{
		{Path: "plugins.auriga-dsh.imBots", ValueJson: `"none"`},
		{Path: "plugins.session-query-sqlite.openAt", ValueJson: `"first-search"`},
		{Path: "plugins.session-query-sqlite.limits.max", ValueJson: `5`},
	}
	spec.Profile.McpServers = map[string]*nodev1.McpServer{
		"team": {Url: "https://ai.example.com/mcp", TokenRef: nodev1.TokenRef_TOKEN_REF_AI_GATEWAY_TOKEN},
	}
	raw, err := renderCordis(t, spec, "I.Example.com")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("rendered patch:\n%s", raw)
	if strings.Contains(raw, "tok-secret") {
		t.Fatal("token value leaked into cordis patch")
	}
	if !strings.Contains(raw, `!!js "'Bearer ' + process.getBuiltinModule('node:fs').readFileSync(\"/run/auriga/ai-gateway.token\", 'utf8').trim()"`) {
		t.Fatalf("mcp header must be a !!js file read:\n%s", raw)
	}
	var rows []map[string]any
	if err := yaml.Unmarshal([]byte(strings.ReplaceAll(raw, "!!js", "")), &rows); err != nil {
		t.Fatalf("not a YAML list: %v\n%s", err, raw)
	}
	if len(rows) != 4 {
		t.Fatalf("rows=%d\n%s", len(rows), raw)
	}
	inserts, _ := rows[0]["insert"].([]any)
	if len(inserts) != 2 {
		t.Fatalf("inserts: %#v", rows[0])
	}
	plugin := inserts[0].(map[string]any)
	if plugin["id"] != renderer.PluginRowID || plugin["name"] != "auriga-dsh/plugin" {
		t.Fatalf("plugin row: %#v", plugin)
	}
	pcfg := plugin["config"].(map[string]any)
	if pcfg["imBots"] != "none" {
		t.Fatalf("plugin config: %#v", pcfg)
	}
	if origin := pcfg["browserOrigin"].(map[string]any); origin["host"] != "inst-c1.i.example.com" || origin["scheme"] != "https" || origin["port"] != 443 {
		t.Fatalf("plugin origin: %#v", origin)
	}
	if portal := pcfg["portalOrigin"].(map[string]any); portal["host"] != "portal.example.com" || portal["scheme"] != "https" || portal["port"] != 443 {
		t.Fatalf("plugin portal: %#v", portal)
	}
	mcp := inserts[1].(map[string]any)
	mcfg := mcp["config"].(map[string]any)
	if mcp["name"] != "@deepseek-ai/dsh-mcp-client" || mcfg["serverName"] != "team" || mcfg["transport"] != "streamable-http" {
		t.Fatalf("mcp row: %#v", mcp)
	}
	web := rows[1]["config"].(map[string]any)
	if rows[1]["id"] != "webserver" || web["host"] != "0.0.0.0" || web["port"] != 7861 {
		t.Fatalf("webserver row: %#v", rows[1])
	}
	rt := rows[2]["config"].(map[string]any)
	if rows[2]["id"] != "web-runtime" || rt["printUrl"] != false || rt["openBrowser"] != false {
		t.Fatalf("web-runtime row: %#v", rows[2])
	}
	if hosts := rt["trustedHosts"].([]any); len(hosts) != 1 || hosts[0] != "inst-c1.i.example.com:443" {
		t.Fatalf("web-runtime trustedHosts: %#v", rt["trustedHosts"])
	}
	other := rows[3]
	ocfg := other["config"].(map[string]any)
	if other["id"] != "session-query-sqlite" || ocfg["openAt"] != "first-search" || ocfg["limits"].(map[string]any)["max"] != 5 {
		t.Fatalf("override row: %#v", other)
	}
}

func TestCordisPatchNativeRuntimeBindsLoopback(t *testing.T) {
	spec := baseSpec()
	spec.Runtime = nodev1.RuntimeKind_RUNTIME_KIND_LINUX_PROCESS
	raw, err := renderCordis(t, spec, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(raw, "host: 127.0.0.1") || !strings.Contains(raw, "192.0.2.10:8080") {
		t.Fatalf("native runtime patch:\n%s", raw)
	}
	if strings.Contains(raw, "imBots") {
		t.Fatal("imBots must only come from an explicit spec declaration")
	}
}

func TestCordisPatchRejectsReservedAndMalformed(t *testing.T) {
	cases := []struct{ Path, ValueJson string }{
		{Path: "plugins.webserver.host", ValueJson: `"0.0.0.0"`},
		{Path: "plugins.connection.trustedHosts", ValueJson: `["evil.example.com"]`},
		{Path: "plugins.web-runtime.trustedHosts", ValueJson: `["evil.example.com"]`},
		{Path: "plugins.auriga-dsh.browserOrigin.host", ValueJson: `"evil.example.com"`},
		{Path: "plugins.auriga-dsh.browserOrigin", ValueJson: `null`},
		{Path: "plugins.auriga-dsh.trustedHosts", ValueJson: `["evil.example.com"]`},
		{Path: "plugins.auriga-mcp-team.url", ValueJson: `"https://evil.example.com"`},
		{Path: "server.host", ValueJson: `"0.0.0.0"`},
		{Path: "plugins.auriga-dsh", ValueJson: `{}`},
		{Path: "plugins.auriga-dsh.mode", ValueJson: `not-json`},
		{Path: "plugins.auriga-dsh.mode", ValueJson: `1 2`},
		{Path: "plugins.auriga-dsh.note", ValueJson: `{"__jsExpr":"process.exit(1)"}`},
		{Path: "plugins.auriga-dsh.note", ValueJson: `{"nested":[{"__jsExpr":"process.exit(1)"}]}`},
	}
	for _, c := range cases {
		spec := baseSpec()
		spec.Profile.Patches = []*nodev1.ConfigPatch{{Path: c.Path, ValueJson: c.ValueJson}}
		if _, err := renderCordis(t, spec, "i.example.com"); err == nil {
			t.Fatalf("expected rejection for %s=%s", c.Path, c.ValueJson)
		}
	}
	spec := baseSpec()
	spec.Profile.ModelProviders = map[string]*nodev1.ModelProvider{"team": {BaseUrl: "https://ai.example.com/v1"}}
	if _, err := renderCordis(t, spec, ""); err == nil {
		t.Fatal("model providers must fail closed until rendered into settings")
	}
	spec = baseSpec()
	spec.Profile.McpServers = map[string]*nodev1.McpServer{"team": {Url: "https://ai.example.com/mcp"}}
	if _, err := renderCordis(t, spec, ""); err == nil {
		t.Fatal("mcp without token_ref must be rejected")
	}
	if _, err := renderer.BrowserOrigin("inst-c1", deploymentconfig.Config{}); err == nil {
		t.Fatal("invalid parent domain accepted")
	}
}

func TestCordisPatchStringValuesStayData(t *testing.T) {
	spec := baseSpec()
	spec.Profile.Patches = []*nodev1.ConfigPatch{{Path: "plugins.auriga-dsh.note", ValueJson: `"x: y # z"`}}
	raw, err := renderCordis(t, spec, "")
	if err != nil {
		t.Fatal(err)
	}
	var rows []map[string]any
	if err := yaml.Unmarshal([]byte(raw), &rows); err != nil {
		t.Fatal(err)
	}
	cfg := rows[0]["insert"].([]any)[0].(map[string]any)["config"].(map[string]any)
	if cfg["note"] != "x: y # z" {
		t.Fatalf("string value changed: %#v", cfg["note"])
	}
}

func TestCordisPatchRejectsExpressionPaths(t *testing.T) {
	for _, path := range []string{
		"plugins.auriga-dsh.__jsExpr",
		"plugins.auriga-dsh.note.__jsExpr",
		"plugins.session-query-sqlite.note.__jsExpr",
		"plugins.session-query-sqlite.__jsExpr.note",
	} {
		t.Run(path, func(t *testing.T) {
			spec := baseSpec()
			spec.Profile.Patches = []*nodev1.ConfigPatch{{Path: path, ValueJson: `"2 + 2"`}}
			if _, err := renderCordis(t, spec, ""); err == nil || !strings.Contains(err.Error(), "Loader expression") {
				t.Fatalf("expected Loader expression rejection, got %v", err)
			}
		})
	}
}

func testAccess(t *testing.T, parent string) deploymentconfig.Config {
	t.Helper()
	name := "ip-http.json"
	if parent != "" {
		name = "domain-proxy.json"
	}
	raw, err := os.ReadFile(filepath.Join("../../../../contracts/deployment/fixtures", name))
	if err != nil {
		t.Fatal(err)
	}
	if parent != "" {
		raw = []byte(strings.ReplaceAll(string(raw), "i.example.com", strings.ToLower(parent)))
	}
	access, err := deploymentconfig.Load(raw)
	if err != nil {
		t.Fatal(err)
	}
	return access
}

func TestBrowserOriginRenderMatrix(t *testing.T) {
	for _, mode := range []string{"ip", "domain"} {
		for _, scheme := range []string{"http", "https"} {
			for _, nonDefault := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/nonDefault=%t", mode, scheme, nonDefault), func(t *testing.T) {
					file := "ip-http.json"
					if mode == "domain" {
						file = "domain-proxy.json"
					}
					raw, err := os.ReadFile(filepath.Join("../../../../contracts/deployment/fixtures", file))
					if err != nil {
						t.Fatal(err)
					}
					var value map[string]any
					if err := json.Unmarshal(raw, &value); err != nil {
						t.Fatal(err)
					}
					port := 80
					if scheme == "https" {
						port = 443
					}
					if nonDefault {
						port = 8443
					}
					value["insecureHttpAcknowledged"] = true
					host := "inst-c1.i.example.com"
					if mode == "domain" {
						sub := value["instances"].(map[string]any)
						sub["scheme"] = scheme
						sub["port"] = port
						for _, field := range []string{"portalOrigin", "issuerOrigin"} {
							o := value[field].(map[string]any)
							o["scheme"] = scheme
							o["port"] = port
						}
					} else {
						host = "192.0.2.10"
						for _, field := range []string{"portalOrigin", "issuerOrigin", "adminOrigin"} {
							o := value[field].(map[string]any)
							o["scheme"] = scheme
							o["port"] = port
							if field == "adminOrigin" {
								o["port"] = port + 1
							}
						}
						for _, field := range []string{"portal", "admin"} {
							value["listeners"].(map[string]any)[field].(map[string]any)["tls"] = scheme == "https"
						}
					}
					raw, _ = json.Marshal(value)
					access, err := deploymentconfig.Load(raw)
					if err != nil {
						t.Fatal(err)
					}
					root := t.TempDir()
					spec := baseSpec()
					got, err := renderer.Render(spec, renderer.Options{MetaDir: root, Access: access})
					if err != nil {
						t.Fatal(err)
					}
					patch, err := os.ReadFile(got.CordisPatchFile)
					if err != nil {
						t.Fatal(err)
					}
					var rows []map[string]any
					if err := yaml.Unmarshal(patch, &rows); err != nil {
						t.Fatal(err)
					}
					cfg := rows[0]["insert"].([]any)[0].(map[string]any)["config"].(map[string]any)
					origin := cfg["browserOrigin"].(map[string]any)
					if origin["scheme"] != scheme || origin["host"] != host || origin["port"] != port {
						t.Fatalf("browser: %#v", origin)
					}
					portal := cfg["portalOrigin"].(map[string]any)
					wantPortal := access.PortalOrigin()
					if portal["scheme"] != wantPortal.Scheme || portal["host"] != wantPortal.Host || portal["port"] != int(wantPortal.Port) {
						t.Fatalf("portal: %#v, want %#v", portal, wantPortal)
					}
					native := rows[2]["config"].(map[string]any)["trustedHosts"].([]any)
					if len(native) != 1 || native[0] != fmt.Sprintf("%s:%d", host, port) {
						t.Fatalf("native: %#v", native)
					}
					if strings.Contains(string(patch), access.InstanceControl().DnsAlias) {
						t.Fatal("internal alias entered browser config")
					}
					env, err := os.ReadFile(got.EnvFile)
					if err != nil {
						t.Fatal(err)
					}
					if !strings.Contains(string(env), "AURIGA_PLATFORM_URL=http://auriga-instance.internal:8081\n") {
						t.Fatalf("heartbeat not internal: %s", env)
					}
				})
			}
		}
	}
	if _, err := renderer.Render(baseSpec(), renderer.Options{MetaDir: t.TempDir()}); err == nil {
		t.Fatal("unvalidated config accepted")
	}
	for _, id := range []string{"UPPER", "bad_label", "-bad", "bad-"} {
		if _, err := renderer.BrowserOrigin(id, testAccess(t, "i.example.com")); err == nil {
			t.Fatalf("invalid DNS instance accepted: %s", id)
		}
	}
}

func TestCordisPatchRejectsPortalOriginOverrides(t *testing.T) {
	for _, path := range []string{
		"plugins.auriga-dsh.portalOrigin",
		"plugins.auriga-dsh.portalOrigin.scheme",
		"plugins.auriga-dsh.portalOrigin.host",
		"plugins.auriga-dsh.portalOrigin.port",
		"plugins.auriga-dsh.portalOrigin.extra",
		"plugins.auriga-dsh.portalOrigin.host.nested",
		"plugins.auriga-dsh",
		"plugins.auriga-dsh.portalOrigin/host",
		"plugins.auriga-dsh.portalOrigin[host]",
	} {
		for _, value := range []string{`null`, `"https://evil.example.com"`, `{"scheme":"https","host":"evil.example.com","port":443}`, `{"portalOrigin":{"scheme":"https","host":"evil.example.com","port":443}}`} {
			t.Run(path+"="+value, func(t *testing.T) {
				spec := baseSpec()
				spec.Profile.Patches = []*nodev1.ConfigPatch{{Path: path, ValueJson: value}}
				if _, err := renderCordis(t, spec, "i.example.com"); err == nil {
					t.Fatal("profile portal override accepted")
				}
			})
		}
	}
}
