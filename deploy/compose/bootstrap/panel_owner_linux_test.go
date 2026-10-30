package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestPanelOwnerEnvironmentIsBoundToKeyMetadata(t *testing.T) {
	out := t.TempDir()
	if err := os.WriteFile(filepath.Join(out, "panel-signing.key"), []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	owner, err := panelOwnerEnvironment(out)
	if err != nil {
		t.Fatal(err)
	}
	keys, ca, access := []byte("KEY=fixture\n"), []byte("fixture-ca\n"), []byte("ACCESS=fixture\n")
	prefix := "KEY=fixture\nAURIGA_AGENT_CA_SHA256=fixture-ca\n"
	for _, env := range []string{prefix + string(access), prefix + string(owner) + string(access)} {
		if !matchesPanelComposeEnvironment(out, keys, ca, access, []byte(env)) {
			t.Fatal("trusted environment rejected")
		}
	}
	valid := []byte(prefix + string(owner) + string(access))
	for _, env := range [][]byte{
		bytes.Replace(valid, []byte("AURIGA_PANEL_UID="), []byte("AURIGA_PANEL_UID=9"), 1),
		append(append([]byte{}, valid...), []byte("AURIGA_PANEL_UID=0\n")...),
		append(append([]byte{}, valid...), []byte("COMPOSE_FILE=foreign\n")...),
	} {
		if matchesPanelComposeEnvironment(out, keys, ca, access, env) {
			t.Fatal("caller-selected environment accepted")
		}
	}
}
