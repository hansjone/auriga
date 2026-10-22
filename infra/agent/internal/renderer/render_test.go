package renderer_test

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hansjone/auriga/infra/agent/internal/identity"
	"github.com/hansjone/auriga/infra/agent/internal/renderer"
	nodev1 "github.com/hansjone/auriga/infra/gen/auriga/node/v1"
)

func TestRenderDeterministicAndSecretPerms(t *testing.T) {
	priv, pub, err := identity.GenerateSealingKey()
	if err != nil {
		t.Fatal(err)
	}
	token := []byte("inst-token-value")
	sealed, err := renderer.SealForTest(pub, "inst_r1", "instance_token", 7, token)
	if err != nil {
		t.Fatal(err)
	}
	gwSealed, err := renderer.SealForTest(pub, "inst_r1", "ai_gateway_token", 7, []byte("gw-token"))
	if err != nil {
		t.Fatal(err)
	}
	_, edPub, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	spec := &nodev1.InstanceSpec{
		InstanceId: "inst_r1",
		TenantId:   "t1",
		OwnerId:    "u1",
		Generation: 7,
		Plugins: []*nodev1.PluginRef{
			{Name: "auriga-dsh", Version: "1.0.0", Level: nodev1.PluginLevel_PLUGIN_LEVEL_L0, Enabled: true},
		},
		Profile: &nodev1.Profile{
			ListenPort: 7860,
			McpServers: map[string]*nodev1.McpServer{
				"team": {Url: "https://edge.example.com/ai/mcp", TokenRef: nodev1.TokenRef_TOKEN_REF_INSTANCE_TOKEN},
			},
			Env: map[string]string{"CUSTOM_FLAG": "1"},
		},
		Network: &nodev1.Network{EgressProxy: "http://egress.example.com:3128"},
		Secrets: &nodev1.Secrets{
			InstanceToken:  sealed,
			AiGatewayToken: gwSealed,
			IdentityKeys: []*nodev1.IdentityPublicKey{
				{Kid: "k1", Alg: "EdDSA", PublicKey: edPub},
			},
		},
	}
	root := t.TempDir()
	meta := filepath.Join(root, "meta")
	home := filepath.Join(root, "home")
	_ = os.MkdirAll(home, 0o700)
	opt := renderer.Options{
		MetaDir:        meta,
		HomeDir:        home,
		Access:         testAccess(t, ""),
		SealingPrivate: priv,
	}
	r1, err := renderer.Render(spec, opt)
	if err != nil {
		t.Fatal(err)
	}
	r2, err := renderer.Render(spec, opt)
	if err != nil {
		t.Fatal(err)
	}
	if r1.Digest != r2.Digest {
		t.Fatalf("digest not deterministic")
	}
	got, err := os.ReadFile(r1.InstanceTokenFile)
	if err != nil || !bytes.Equal(got, token) {
		t.Fatalf("token file: %q %v", got, err)
	}
	assertSecretRestricted(t, r1.InstanceTokenFile)
	env, _ := os.ReadFile(r1.EnvFile)
	if strings.Contains(string(env), string(token)) {
		t.Fatal("token leaked into env file")
	}
	if !strings.Contains(string(env), "AURIGA_IDENTITY_KEYS_FILE="+renderer.ContainerIdentityKeys) {
		t.Fatalf("env should use container paths: %s", env)
	}
	if r1.AIGatewayTokenFile == "" {
		t.Fatal("expected ai-gateway token")
	}
	// Rotating gw token must change digest.
	gw2, _ := renderer.SealForTest(pub, "inst_r1", "ai_gateway_token", 7, []byte("gw-token-2"))
	spec.Secrets.AiGatewayToken = gw2
	r3, err := renderer.Render(spec, opt)
	if err != nil {
		t.Fatal(err)
	}
	if r3.Digest == r1.Digest {
		t.Fatal("digest should include ai-gateway.token")
	}
}

func TestRejectYAMLInjectionAndReservedEnv(t *testing.T) {
	priv, pub, _ := identity.GenerateSealingKey()
	sealed, _ := renderer.SealForTest(pub, "inst_x", "instance_token", 1, []byte("t"))
	base := func() *nodev1.InstanceSpec {
		return &nodev1.InstanceSpec{
			InstanceId: "inst_x", TenantId: "t1", OwnerId: "u1", Generation: 1,
			Secrets: &nodev1.Secrets{InstanceToken: sealed},
			Profile: &nodev1.Profile{},
		}
	}
	root := t.TempDir()
	opt := renderer.Options{MetaDir: filepath.Join(root, "meta"), HomeDir: filepath.Join(root, "home"), SealingPrivate: priv, Access: testAccess(t, "")}
	_ = os.MkdirAll(opt.HomeDir, 0o700)

	spec := base()
	spec.Profile.Patches = []*nodev1.ConfigPatch{{Path: "a\nb", ValueJson: "1"}}
	if _, err := renderer.Render(spec, opt); err == nil {
		t.Fatal("newline in path")
	}
	spec = base()
	spec.Profile.Patches = []*nodev1.ConfigPatch{{Path: "ok", ValueJson: "!!js/function x"}}
	if _, err := renderer.Render(spec, opt); err == nil {
		t.Fatal("yaml tag in value")
	}
	spec = base()
	spec.Profile.Env = map[string]string{"AURIGA_PLATFORM_URL": "https://evil.example.com"}
	if _, err := renderer.Render(spec, opt); err == nil {
		t.Fatal("reserved env")
	}
	spec = base()
	spec.Profile.Env = map[string]string{"CUSTOM": "a\nb"}
	if _, err := renderer.Render(spec, opt); err == nil {
		t.Fatal("newline in env value")
	}
	for _, key := range []string{"PATH", "HTTPS_PROXY", "LD_AUDIT", "NODE_PATH"} {
		spec = base()
		spec.Profile.Env = map[string]string{key: "x"}
		if _, err := renderer.Render(spec, opt); err == nil {
			t.Fatalf("reserved env %s", key)
		}
	}
}

func TestUnsealWrongGenerationFails(t *testing.T) {
	priv, pub, err := identity.GenerateSealingKey()
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := renderer.SealForTest(pub, "i", "instance_token", 1, []byte("x"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = renderer.Unseal(priv, "i", "instance_token", 2, sealed)
	if err == nil {
		t.Fatal("expected unseal failure")
	}
}
