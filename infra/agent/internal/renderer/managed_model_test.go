package renderer_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hansjone/auriga/infra/agent/internal/identity"
	"github.com/hansjone/auriga/infra/agent/internal/renderer"
	nodev1 "github.com/hansjone/auriga/infra/gen/auriga/node/v1"
)

func TestManagedModelNativeConfigurationAndSecretRemoval(t *testing.T) {
	priv, pub, err := identity.GenerateSealingKey()
	if err != nil {
		t.Fatal(err)
	}
	spec := baseSpec()
	spec.Secrets = &nodev1.Secrets{}
	opt := renderer.Options{MetaDir: t.TempDir(), Access: testAccess(t, ""), SealingPrivate: priv}
	credential := "amc." + spec.TenantId + "." + spec.OwnerId + "." + spec.InstanceId + ".01900000-0000-7000-8000-000000000003." + strings.Repeat("a", 43)
	material := map[string]any{"tenantId": spec.TenantId, "ownerId": spec.OwnerId, "instanceId": spec.InstanceId, "credentialVersion": 1, "credential": credential, "baseUrl": "https://portal.example.com/ai/v1", "models": []any{map[string]any{"id": "pool-a"}, map[string]any{"id": "pool-b"}}}
	raw, err := json.Marshal(material)
	if err != nil {
		t.Fatal(err)
	}
	spec.Secrets.ModelCredential, err = renderer.SealForTest(pub, spec.InstanceId, "model_credential", spec.Generation, raw)
	if err != nil {
		t.Fatal(err)
	}
	result, err := renderer.Render(spec, opt)
	if err != nil {
		t.Fatal(err)
	}
	tokenPath := filepath.Join(opt.MetaDir, "model-credential.json")
	assertSecretRestricted(t, tokenPath)
	for _, file := range []string{result.CordisPatchFile, result.EnvFile, result.PackageJSONFile} {
		content, e := os.ReadFile(file)
		if e != nil || bytes.Contains(content, []byte(credential)) {
			t.Fatal("model secret escaped private file")
		}
	}
	patch, err := os.ReadFile(result.CordisPatchFile)
	if err != nil || !bytes.Contains(patch, []byte("openai-completions")) || !bytes.Contains(patch, []byte("pool-b")) {
		t.Fatal("native provider missing")
	}
	oldDigest := result.Digest
	credential = strings.TrimSuffix(credential, strings.Repeat("a", 43)) + strings.Repeat("b", 43)
	material["credential"] = credential
	raw, err = json.Marshal(material)
	if err != nil {
		t.Fatal(err)
	}
	spec.Generation++
	if _, err = renderer.Render(spec, opt); err == nil {
		t.Fatal("previous generation ciphertext accepted")
	}
	spec.Secrets.ModelCredential, err = renderer.SealForTest(pub, spec.InstanceId, "model_credential", spec.Generation, raw)
	if err != nil {
		t.Fatal(err)
	}
	result, err = renderer.Render(spec, opt)
	if err != nil || result.Digest == oldDigest {
		t.Fatal("rotation did not change render digest")
	}
	material["ownerId"] = "other"
	raw, err = json.Marshal(material)
	if err != nil {
		t.Fatal(err)
	}
	spec.Secrets.ModelCredential, err = renderer.SealForTest(pub, spec.InstanceId, "model_credential", spec.Generation, raw)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = renderer.Render(spec, opt); err == nil {
		t.Fatal("foreign owner accepted")
	}
	spec.Secrets.ModelCredential = nil
	if _, err = renderer.Render(spec, opt); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(tokenPath); !os.IsNotExist(err) {
		t.Fatal("revoked model secret retained")
	}
}
