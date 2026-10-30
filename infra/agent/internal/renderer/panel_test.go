package renderer_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hansjone/auriga/infra/agent/internal/renderer"
	nodev1 "github.com/hansjone/auriga/infra/gen/auriga/node/v1"
)

func TestRenderTrustedPanelKeys(t *testing.T) {
	root := t.TempDir()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	raw := []byte(fmt.Sprintf(`{"keys":[{"kid":"panel-1","kty":"OKP","crv":"Ed25519","alg":"EdDSA","x":"%s"}]}`, base64.RawURLEncoding.EncodeToString(pub)))
	file := filepath.Join(root, "panel-keys.json")
	if err := os.WriteFile(file, raw, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AURIGA_AGENT_PANEL_KEYS_FILE", file)
	spec := &nodev1.InstanceSpec{InstanceId: "inst-panel", TenantId: "t1", OwnerId: "u1"}
	opt := renderer.Options{MetaDir: filepath.Join(root, "meta"), Access: testAccess(t, "")}
	result, err := renderer.Render(spec, opt)
	if err != nil {
		t.Fatal(err)
	}
	rendered, err := os.ReadFile(filepath.Join(opt.MetaDir, "panel-keys.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(rendered) != string(raw) {
		t.Fatal("trusted public keys changed")
	}
	env, err := os.ReadFile(result.EnvFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(env), "AURIGA_PANEL_KEYS_FILE=/run/auriga/panel-keys.json") {
		t.Fatal("panel key path missing")
	}
	first := result.Digest
	if err := os.WriteFile(file, []byte(strings.ReplaceAll(string(raw), "panel-1", "panel-2")), 0600); err != nil {
		t.Fatal(err)
	}
	result, err = renderer.Render(spec, opt)
	if err != nil {
		t.Fatal(err)
	}
	if first == result.Digest {
		t.Fatal("public key changes absent from digest")
	}
	for _, bad := range []string{`{"keys":[]}`, `{"keys":[{"kid":"p","kty":"OKP","crv":"Ed25519","x":"invalid"}]}`, strings.Replace(string(raw), `"alg":"EdDSA"`, `"d":"private","alg":"EdDSA"`, 1)} {
		if err := os.WriteFile(file, []byte(bad), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := renderer.Render(spec, opt); err == nil {
			t.Fatal("invalid/private panel keys accepted")
		}
	}
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	if _, err := renderer.Render(spec, opt); err == nil {
		t.Fatal("missing configured key file accepted")
	}
}
