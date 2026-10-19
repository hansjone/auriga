//go:build unix

package renderer_test

import (
	"os"
	"testing"
)

func assertSecretRestricted(t *testing.T, path string) {
	t.Helper()
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm()&0o077 != 0 {
		t.Fatalf("token perms too open: %v", st.Mode())
	}
}
