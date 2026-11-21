package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestSharedEmptyAdapterCannotShadowLegacyUACIdentitySources(t *testing.T) {
	for _, leaf := range []string{"uac.properties", "members.json"} {
		t.Run(leaf, func(t *testing.T) {
			base := t.TempDir()
			if err := os.Mkdir(filepath.Join(base, "uac"), 0700); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(base, "uac", leaf)
			original := []byte("synthetic existing company identity source")
			if err := os.WriteFile(path, original, 0600); err != nil {
				t.Fatal(err)
			}
			for _, spelling := range []string{"{}", "{}\n", "{ }", " \t\r\n{ \n\t }\r\n"} {
				for _, teamOnly := range []bool{false, true} {
					if err := sharedPreserveUACSource(base, []byte(spelling), teamOnly); err == nil {
						t.Fatalf("empty object spelling %q shadowed %s (teamOnly=%t)", spelling, leaf, teamOnly)
					}
				}
			}
			approved := []byte(sharedApprovedAdapterFixture)
			before := bytes.Clone(approved)
			if err := sharedPreserveUACSource(base, approved, false); err != nil {
				t.Fatal("complete approved company configuration rejected", err)
			}
			after, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(after, original) || !bytes.Equal(approved, before) {
				t.Fatal("existing or approved source bytes changed")
			}
		})
	}
	base := t.TempDir()
	for _, spelling := range []string{"{}", "{}\n", "{ }", " \t\r\n{ \n\t }\r\n"} {
		if err := sharedPreserveUACSource(base, []byte(spelling), true); err != nil {
			t.Fatal("explicit fresh team-only installation rejected", err)
		}
	}
	for _, invalid := range []string{"null", "[]", "{} {}", `{"qr":1,"qr":2}`} {
		if err := sharedPreserveUACSource(base, []byte(invalid), false); err == nil {
			t.Fatal("non-object or ambiguous adapter accepted")
		}
	}
}
