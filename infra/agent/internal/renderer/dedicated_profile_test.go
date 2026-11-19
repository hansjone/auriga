package renderer

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDedicatedRendererExportsClosedPreparation(t *testing.T) {
	profile, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "deploy", "nas", "dedicated-runtime-profile.example.yml"))
	if err != nil {
		t.Fatal(err)
	}
	meta := t.TempDir()
	if output := os.Getenv("AURIGA_DEDICATED_PREPARATION_FIXTURE"); output != "" {
		if !filepath.IsAbs(output) {
			t.Fatal("absolute output required")
		}
		meta = filepath.Join(output, "input")
	}
	binding := json.RawMessage(`{"tenantId":"00000000-0000-4000-8000-000000000001","memberId":"00000000-0000-4000-8000-000000000003","placementId":"00000000-0000-4000-8000-000000000003","physicalInstanceId":"00000000-0000-4000-8000-000000000002","mode":"dedicated","epoch":1,"revision":1}`)
	view := append([]byte(`{"schemaVersion":1,"binding":`), binding...)
	view = append(view, []byte(`,"viewId":"00000000-0000-4000-8000-000000000004","source":"platform","authorizationSetDigest":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","resources":[]}`)...)
	intent := append([]byte(`{"schemaVersion":1,"view":`), view...)
	intent = append(intent, []byte(`,"resources":[]}`)...)
	// All keys/integers in this empty fixture are ASCII/safe. Sorted JSON is the
	// same canonical form as the Host; production uses the materializer's JCS.
	var value map[string]any
	if err = json.Unmarshal(intent, &value); err != nil {
		t.Fatal(err)
	}
	intent, err = json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	input := DedicatedPreparation{SchemaVersion: 1, AttemptID: "00000000-0000-4000-8000-000000000005", Binding: binding, View: view, IntentDigest: fmt.Sprintf("sha256:%x", sha256.Sum256(intent)), ImageID: "sha256:" + strings.Repeat("b", 64)}
	digest, err := RenderDedicatedPool(profile, "00000000-0000-4000-8000-000000000002", meta, 7860, input, intent)
	if err != nil || len(digest) != 64 {
		t.Fatal(digest, err)
	}
	if again, err := RenderDedicatedPool(profile, "00000000-0000-4000-8000-000000000002", meta, 7860, input, intent); err != nil || digest != again {
		t.Fatal("same immutable input changed", err)
	}
	input.AttemptID = "00000000-0000-4000-8000-000000000006"
	if _, err = RenderDedicatedPool(profile, "00000000-0000-4000-8000-000000000002", meta, 7860, input, intent); err == nil {
		t.Fatal("existing input was overwritten")
	}
}

func TestDedicatedFrozenProfileSealAndTrustedSlots(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "deploy", "nas", "dedicated-runtime-profile.example.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if err = ValidateDedicatedPoolProfile(raw); err != nil {
		t.Fatal("frozen dedicated closure rejected", err)
	}
	trusted := strings.ReplaceAll(string(raw), "00000000-0000-4000-8000-000000000001", "00000000-0000-4000-8000-000000000009")
	trusted = strings.Replace(trusted, "host: 127.0.0.1", "host: 0.0.0.0", 1)
	trusted = strings.Replace(trusted, "port: 7860", "port: 7871", 1)
	trusted = strings.Replace(trusted, "model: gpt-6.1-sol", "model: approved-platform-model", 1)
	if err = ValidateDedicatedPoolProfile([]byte(trusted)); err != nil {
		t.Fatal("trusted renderer slot rejected", err)
	}
	for _, unsafe := range []string{
		strings.Replace(string(raw), DedicatedModule, "auriga-dsh", 1),
		strings.Replace(string(raw), "policy: ask", "policy: never", 1),
		strings.Replace(string(raw), "includeDefaultRoots: true", "includeDefaultRoots: false", 1),
		strings.Replace(string(raw), "runnerCommand: []", "runnerCommand: [unapproved]", 1),
		strings.Replace(string(raw), "fallbackRegistries: []", "fallbackRegistries: [https://example.com]", 1),
		strings.Replace(string(raw), "maxConcurrentJobsPerOwner: 4", "maxConcurrentJobsPerOwner: 40", 1),
		string(raw) + "---\n[]\n",
	} {
		if err = ValidateDedicatedPoolProfile([]byte(unsafe)); err == nil {
			t.Fatal("modified dedicated baseline accepted")
		}
	}
	if err = ValidateProductionPoolProfile(raw); err == nil {
		t.Fatal("dedicated closure widened shared admission")
	}
}
