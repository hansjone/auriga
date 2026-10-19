package ingress

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateRelPath(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in      string
		wantErr string
	}{
		{"a/b", ""},
		{".", ""},
		{"", ""},
		{"../x", CodePathEscape},
		{"a/../../x", CodePathEscape},
		{"/abs", CodePathEscape},
		{"C:/windows", CodePathEscape},
		{"a\x00b", CodePathInvalid},
	}
	for _, tc := range cases {
		_, err := ValidateRelPath(tc.in)
		if tc.wantErr == "" {
			if err != nil {
				t.Fatalf("%q: unexpected err %v", tc.in, err)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
			t.Fatalf("%q: want %s, got %v", tc.in, tc.wantErr, err)
		}
	}
}

func TestResolveUnderRoot_SymlinkEscape(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "leak")
	if err := os.Symlink(outside, link); err != nil {
		t.Skip("symlink not supported:", err)
	}
	_, err := ResolveUnderRoot(root, "leak/secret")
	if err == nil || !strings.Contains(err.Error(), CodePathEscape) {
		t.Fatalf("want path escape, got %v", err)
	}
}

func TestResolveUnderRoot_DotDot(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	_, err := ResolveUnderRoot(root, "a/../../../etc/passwd")
	if err == nil || !strings.Contains(err.Error(), CodePathEscape) {
		t.Fatalf("want escape, got %v", err)
	}
}
