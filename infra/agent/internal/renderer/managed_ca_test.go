package renderer_test

import (
	"encoding/pem"
	"github.com/hansjone/auriga/infra/agent/internal/renderer"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestManagedInstallationCAIsPublicReservedAndIncludedInDigest(t *testing.T) {
	server := httptest.NewTLSServer(nil)
	server.Close()
	raw := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
	root := t.TempDir()
	file := filepath.Join(root, "ca.pem")
	if err := os.WriteFile(file, raw, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AURIGA_AGENT_MANAGED_CA_FILE", file)
	spec := baseSpec()
	opt := renderer.Options{MetaDir: filepath.Join(root, "meta"), Access: testAccess(t, "")}
	result, err := renderer.Render(spec, opt)
	if err != nil {
		t.Fatal(err)
	}
	env, err := os.ReadFile(result.EnvFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(env), "NODE_EXTRA_CA_CERTS=/run/auriga/managed-ca.pem") {
		t.Fatal("managed TLS trust not projected")
	}
	got, err := os.ReadFile(filepath.Join(opt.MetaDir, "managed-ca.pem"))
	if err != nil || string(got) != string(raw) {
		t.Fatal("CA projection changed")
	}
	spec.Profile.Env = map[string]string{"NODE_EXTRA_CA_CERTS": "/attacker.pem"}
	if _, err = renderer.Render(spec, opt); err == nil {
		t.Fatal("spec replaced installation trust")
	}
	delete(spec.Profile.Env, "NODE_EXTRA_CA_CERTS")
	for _, invalid := range [][]byte{[]byte("not a certificate"), append(raw, []byte("PRIVATE KEY")...)} {
		if err := os.WriteFile(file, invalid, 0600); err != nil {
			t.Fatal(err)
		}
		if _, err = renderer.Render(spec, opt); err == nil {
			t.Fatal("invalid public CA accepted")
		}
	}
	t.Setenv("AURIGA_AGENT_MANAGED_CA_FILE", "")
	without, err := renderer.Render(spec, opt)
	if err != nil {
		t.Fatal(err)
	}
	if without.Digest == result.Digest {
		t.Fatal("CA not bound to rendered digest")
	}
	if _, err = os.Stat(filepath.Join(opt.MetaDir, "managed-ca.pem")); !os.IsNotExist(err) {
		t.Fatal("stale CA survived removal")
	}
}
