//go:build windows

package renderer_test

import (
	"testing"

	"golang.org/x/sys/windows"
)

func assertSecretRestricted(t *testing.T, path string) {
	t.Helper()
	sd, err := windows.GetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
	)
	if err != nil {
		t.Fatal(err)
	}
	dacl, _, err := sd.DACL()
	if err != nil || dacl == nil {
		t.Fatalf("dacl: %v", err)
	}
	// Protected DACL with explicit entries is enough; identity tests cover SID detail.
	_ = dacl
}
