package runtimebundle

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

func setTestACL(t *testing.T, path string, weak bool) {
	t.Helper()
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	sid := user.User.Sid
	if weak {
		sid, err = windows.CreateWellKnownSid(windows.WinWorldSid)
		if err != nil {
			t.Fatal(err)
		}
	}
	acl, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{{AccessPermissions: windows.GENERIC_ALL, AccessMode: windows.GRANT_ACCESS, Trustee: windows.TRUSTEE{TrusteeForm: windows.TRUSTEE_IS_SID, TrusteeType: windows.TRUSTEE_IS_USER, TrusteeValue: windows.TrusteeValueFromSID(sid)}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil); err != nil {
		t.Fatal(err)
	}
}

func protectPrivate(t *testing.T, path string) { t.Helper(); setTestACL(t, path, false) }

func weakenPrivate(t *testing.T, path string) { t.Helper(); setTestACL(t, path, true) }

func TestWindowsWeakPrivateDACL(t *testing.T) {
	f := fresh(t)
	setTestACL(t, f.o.PrivateKeyPath, true)
	_, err := Sign(f.o)
	invalid(t, err)
}

func TestWindowsJunctionsRejected(t *testing.T) {
	for _, kind := range []string{"bundle", "proof-directory", "ancestor"} {
		t.Run(kind, func(t *testing.T) {
			f := fresh(t)
			signOK(t, f)
			var link, target string
			switch kind {
			case "bundle":
				link, target = f.o.OutputDir, f.o.OutputDir+"-original"
			case "proof-directory":
				link = filepath.Dir(proofFile(&f, "manifest.json"))
				target = filepath.Join(filepath.Dir(f.o.OutputDir), "original-proof")
			case "ancestor":
				link = filepath.Join(filepath.Dir(f.o.OutputDir), "junction")
				target = filepath.Dir(f.o.OutputDir)
			}
			if kind != "ancestor" {
				if err := os.Rename(link, target); err != nil {
					t.Fatal(err)
				}
			}
			// Junction creation does not require Windows symlink privilege.
			if raw, err := exec.Command("cmd", "/c", "mklink", "/J", link, target).CombinedOutput(); err != nil {
				t.Fatalf("create test junction: %v: %s", err, raw)
			}
			if kind == "ancestor" {
				f.o.OutputDir = filepath.Join(link, "bundle")
			}
			_, err := Verify(f.verify())
			invalid(t, err)
		})
	}
}
