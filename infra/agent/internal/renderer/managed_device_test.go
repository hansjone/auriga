package renderer_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/hansjone/auriga/infra/agent/internal/identity"
	"github.com/hansjone/auriga/infra/agent/internal/renderer"
	nodev1 "github.com/hansjone/auriga/infra/gen/auriga/node/v1"
)

func TestManagedDeviceSecretFileRotationAndRemoval(t *testing.T) {
	priv, pub, err := identity.GenerateSealingKey()
	if err != nil {
		t.Fatal(err)
	}
	raw := []byte(`{"credential":"private-managed-device-material"}`)
	sealed, err := renderer.SealForTest(pub, "managed1", "device_credential", 1, raw)
	if err != nil {
		t.Fatal(err)
	}
	spec := &nodev1.InstanceSpec{InstanceId: "managed1", TenantId: "tenant1", OwnerId: "owner1", Generation: 1,
		Secrets: &nodev1.Secrets{DeviceCredential: sealed}}
	opt := renderer.Options{MetaDir: t.TempDir(), Access: testAccess(t, ""), SealingPrivate: priv}
	result, err := renderer.Render(spec, opt)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(opt.MetaDir, "device-credential.json")
	content, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(content, raw) {
		t.Fatal("managed credential not rendered")
	}
	assertSecretRestricted(t, path)
	for _, config := range []string{result.EnvFile, result.CordisPatchFile, result.PackageJSONFile} {
		content, err := os.ReadFile(config)
		if err != nil || bytes.Contains(content, raw) {
			t.Fatal("secret escaped private file")
		}
	}
	env, err := os.ReadFile(result.EnvFile)
	if err != nil || !bytes.Contains(env, []byte("AURIGA_DEVICE_CREDENTIAL_FILE=")) {
		t.Fatal("missing credential file path")
	}
	spec.Generation = 2
	if _, err := renderer.Render(spec, opt); err == nil {
		t.Fatal("old generation ciphertext accepted")
	}
	spec.Secrets.DeviceCredential = nil
	if _, err := renderer.Render(spec, opt); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("revoked credential file retained")
	}
}
