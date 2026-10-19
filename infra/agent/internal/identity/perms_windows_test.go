//go:build windows

package identity_test

import (
	"path/filepath"
	"testing"
	"time"
	"unsafe"

	"github.com/hansjone/auriga/infra/agent/internal/identity"
	"github.com/hansjone/auriga/infra/agent/internal/testutil"
	"golang.org/x/sys/windows"
)

func TestWindowsRestrictFileACL(t *testing.T) {
	ca, err := testutil.NewTestCA()
	if err != nil {
		t.Fatal(err)
	}
	key, err := identity.GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	cert, err := ca.IssueNode("n_acl", &key.PublicKey, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	priv, pub, err := identity.GenerateSealingKey()
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "agent")
	mat := &identity.Material{PrivateKey: key, CertificatePEM: cert, CAPEM: ca.PEM, SealingPrivate: priv, SealingPublic: pub}
	for attempt := 0; attempt < 2; attempt++ {
		if err := identity.Save(dir, mat); err != nil {
			t.Fatal(err)
		}
		for _, path := range []string{dir, filepath.Join(dir, identity.KeyFileName), filepath.Join(dir, identity.SealingFileName), filepath.Join(dir, identity.CertFileName), filepath.Join(dir, identity.CAFileName)} {
			t.Run(filepath.Base(path), func(t *testing.T) { assertRestrictedDACL(t, path) })
		}
	}
}

// FILE_ALL_ACCESS is mapped by Windows when GENERIC_ALL is applied to a file.
const fileAllAccess windows.ACCESS_MASK = 0x001f01ff

func assertRestrictedDACL(t *testing.T, path string) {
	t.Helper()
	token := windows.GetCurrentProcessToken()
	user, err := token.GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"S-1-5-18": false, "S-1-5-32-544": false, user.User.Sid.String(): false}
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	control, _, err := sd.Control()
	if err != nil {
		t.Fatal(err)
	}
	if control&windows.SE_DACL_PROTECTED == 0 {
		t.Fatal("DACL inheritance is not protected")
	}
	acl, _, err := sd.DACL()
	if err != nil {
		t.Fatal(err)
	}
	if acl == nil || int(acl.AceCount) != len(want) {
		t.Fatalf("expected exactly %d explicit ACEs, got %v", len(want), acl)
	}
	for i := uint32(0); i < uint32(acl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(acl, i, &ace); err != nil {
			t.Fatal(err)
		}
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
			t.Fatalf("unexpected ACE type %d", ace.Header.AceType)
		}
		if ace.Header.AceFlags != 0 {
			t.Fatalf("inherited or inheritable ACE: %#x", ace.Header.AceFlags)
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart)).String()
		if _, ok := want[sid]; !ok {
			t.Fatalf("unexpected SID %s (Users/Everyone must not be granted)", sid)
		}
		if want[sid] {
			t.Fatalf("duplicate SID %s", sid)
		}
		want[sid] = true
		if ace.Mask&fileAllAccess != fileAllAccess {
			t.Fatalf("SID %s lacks full access: %#x", sid, ace.Mask)
		}
	}
	for sid, seen := range want {
		if !seen {
			t.Errorf("missing SID %s", sid)
		}
	}
}
