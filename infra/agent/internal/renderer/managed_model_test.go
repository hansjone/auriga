package renderer_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hansjone/auriga/infra/agent/internal/identity"
	"github.com/hansjone/auriga/infra/agent/internal/renderer"
	nodev1 "github.com/hansjone/auriga/infra/gen/auriga/node/v1"
	"gopkg.in/yaml.v3"
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
	if runtimeRoot := os.Getenv("AURIGA_DSH_MODEL_RUNTIME"); runtimeRoot != "" {
		ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, "node", "testdata/dsh-model-probe.mjs", runtimeRoot, result.CordisPatchFile, opt.MetaDir, t.TempDir())
		if fixture := os.Getenv("AURIGA_PI_AI_REQUEST_FIXTURE"); fixture != "" {
			cmd.Args = append(cmd.Args, fixture)
		}
		for _, item := range os.Environ() {
			name, _, _ := strings.Cut(item, "=")
			name = strings.ToUpper(name)
			if !strings.HasPrefix(name, "AURIGA_") && !strings.HasPrefix(name, "DSH_") {
				cmd.Env = append(cmd.Env, item)
			}
		}
		output, probeErr := cmd.CombinedOutput()
		t.Logf("native model module probe: %s", output)
		if probeErr != nil {
			t.Fatal(probeErr)
		}
	}
	var rows []map[string]any
	if err = yaml.Unmarshal(patch, &rows); err != nil {
		t.Fatal(err)
	}
	var provider map[string]any
	for _, row := range rows {
		if row["id"] == "llm-pi-ai" {
			provider = row["config"].(map[string]any)["providers"].(map[string]any)["auriga-managed"].(map[string]any)
		}
	}
	if provider == nil {
		t.Fatal("native provider row missing")
	}
	compat, ok := provider["compat"].(map[string]any)
	if !ok || compat["supportsStore"] != false {
		t.Fatal("store must be disabled for the relay")
	}
	if provider["apiKeyEnv"] != "AURIGA_MANAGED_MODEL_KEY" || provider["headers"] != nil {
		t.Fatal("configuration must contain only a credential reference")
	}
	keysPath := filepath.Join(opt.MetaDir, "model-keys.json")
	assertSecretRestricted(t, keysPath)
	keysRaw, err := os.ReadFile(keysPath)
	if err != nil {
		t.Fatal(err)
	}
	var keys struct {
		Version int               `json:"version"`
		Refs    map[string]string `json:"refs"`
	}
	if json.Unmarshal(keysRaw, &keys) != nil || keys.Version != 1 || keys.Refs["AURIGA_MANAGED_MODEL_KEY"] != credential {
		t.Fatal("native credential document mismatch")
	}
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
	if _, err = os.Stat(keysPath); !os.IsNotExist(err) {
		t.Fatal("revoked native credential retained")
	}
}
