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
		},
		Network: &nodev1.Network{EgressProxy: "http://egress.example.com:3128"},
		Secrets: &nodev1.Secrets{
			InstanceToken: sealed,
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
		PlatformURL:    "https://edge.example.com",
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
		t.Fatalf("digest not deterministic: %s vs %s", r1.Digest, r2.Digest)
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
	if !strings.Contains(string(env), "AURIGA_MODE=managed") {
		t.Fatalf("env missing mode: %s", env)
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
