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

	"github.com/hansjone/auriga/infra/agent/internal/identity"
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
	sealingPrivate, sealingPublic, err := identity.GenerateSealingKey()
	if err != nil {
		t.Fatal(err)
	}
	credential := []byte(`{"credential":"private-managed-material"}`)
	sealed, err := renderer.SealForTest(sealingPublic, "inst-panel", "device_credential", 1, credential)
	if err != nil {
		t.Fatal(err)
	}
	spec := &nodev1.InstanceSpec{InstanceId: "inst-panel", TenantId: "t1", OwnerId: "u1", Generation: 1, Secrets: &nodev1.Secrets{DeviceCredential: sealed}}
	opt := renderer.Options{MetaDir: filepath.Join(root, "meta"), Access: testAccess(t, ""), SealingPrivate: sealingPrivate}
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
	devicePath := filepath.Join(opt.MetaDir, "device-credential.json")
	deviceRaw, err := os.ReadFile(devicePath)
	if err != nil || string(deviceRaw) != string(credential) {
		t.Fatal("managed credential missing beside panel keys")
	}
	assertSecretRestricted(t, devicePath)
	if !strings.Contains(string(env), "AURIGA_DEVICE_CREDENTIAL_FILE="+renderer.ContainerDeviceCredential) || strings.Contains(string(env), string(credential)) {
		t.Fatal("invalid managed credential environment")
	}
	first := result.Digest
	spec.Secrets.DeviceCredential, err = renderer.SealForTest(sealingPublic, "inst-panel", "device_credential", 1, []byte(`{"credential":"rotated-managed-material"}`))
	if err != nil {
		t.Fatal(err)
	}
	result, err = renderer.Render(spec, opt)
	if err != nil {
		t.Fatal(err)
	}
	if first == result.Digest {
		t.Fatal("managed credential changes absent from digest when panel keys are present")
	}
	first = result.Digest
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
