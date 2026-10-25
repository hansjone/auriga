package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVersionAndMissingRuntimeConfiguration(t *testing.T) {
	var out, logs bytes.Buffer
	if err := run([]string{"--version"}, &out, &logs); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "auriga-ai-gateway "+version) {
		t.Fatal(out.String())
	}
	if logs.Len() != 0 {
		t.Fatal("version must not initialize services")
	}
	if err := run(nil, &out, &logs); err == nil {
		t.Fatal("missing runtime configuration must fail startup")
	}
}

func TestNetworkRuntimeRequiresCompleteConfiguration(t *testing.T) {
	for _, content := range []string{`{}`, `{"serverAudience":"server-a"}`, `{"unknown":true}`} {
		path := filepath.Join(t.TempDir(), "network.json")
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		var out, logs bytes.Buffer
		err := run([]string{"--network-config", path}, &out, &logs)
		if err == nil || strings.Contains(err.Error(), "flag provided but not defined") {
			t.Fatalf("expected runtime config validation, got %v", err)
		}
	}
}
