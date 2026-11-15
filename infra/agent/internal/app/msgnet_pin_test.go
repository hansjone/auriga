package app

import (
	"bytes"
	"crypto/ed25519"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/hansjone/auriga/infra/agent/internal/identity"
	"github.com/hansjone/auriga/infra/agent/internal/testutil"
	api "github.com/hansjone/auriga/infra/gen/msgnetapi"
)

func TestMsgnetPinCommandExportsOnlyIndependentPublicKey(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "agent")
	if e := identity.EnsureDir(dir); e != nil {
		t.Fatal(e)
	}
	ca, e := testutil.NewTestCA()
	if e != nil {
		t.Fatal(e)
	}
	native, e := identity.GenerateKeyPair()
	if e != nil {
		t.Fatal(e)
	}
	cert, e := ca.IssueNode("enrolled-node", &native.PublicKey, time.Now().Add(time.Hour))
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(dir, identity.CertFileName), cert, 0600); e != nil {
		t.Fatal(e)
	}
	args := []string{"msgnet-pin", "--data-root", root, "--target-id", "fixed-target", "--identity-id", "independent-confirmation", "--identity-revision", "2"}
	var first bytes.Buffer
	if e = RunCLI(args, &first, &bytes.Buffer{}); e != nil {
		t.Fatal(e)
	}
	var pin api.NodeControlPin
	if e = json.Unmarshal(first.Bytes(), &pin); e != nil {
		t.Fatal(e)
	}
	if pin.NodeId != "enrolled-node" || pin.IdentityRevision != "2" || len(pin.AdditionalProperties) != 0 {
		t.Fatal("incorrect public pin")
	}
	der, e := base64.RawURLEncoding.DecodeString(pin.SigningSpki)
	if e != nil {
		t.Fatal(e)
	}
	key, e := x509.ParsePKIXPublicKey(der)
	if e != nil {
		t.Fatal(e)
	}
	if _, ok := key.(ed25519.PublicKey); !ok {
		t.Fatal("native ECDSA reused")
	}
	var second bytes.Buffer
	if e = RunCLI(args, &second, &bytes.Buffer{}); e != nil || !bytes.Equal(first.Bytes(), second.Bytes()) {
		t.Fatal("pin generation rotated identity")
	}
	if _, e = os.Stat(filepath.Join(dir, identity.KeyFileName)); !os.IsNotExist(e) {
		t.Fatal("public export touched native key")
	}
	args[6] = "replacement-confirmation" // identity-id's value, independently enrolled by D.
	args[8] = "3"
	var replacement bytes.Buffer
	if e = RunCLI(args, &replacement, &bytes.Buffer{}); e != nil {
		t.Fatal(e)
	}
	var replacementPin api.NodeControlPin
	if e = json.Unmarshal(replacement.Bytes(), &replacementPin); e != nil {
		t.Fatal(e)
	}
	if replacementPin.SigningSpki == pin.SigningSpki {
		t.Fatal("replacement identity reused compromised key")
	}
}
