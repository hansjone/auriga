package main

import (
	"bytes"
	"encoding/json"
	"github.com/google/uuid"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSharedMessageRuntimeIsInstalledByTheRealMain(t *testing.T) {
	content := map[string]any{"listen": "127.0.0.1:0", "origin": "http://127.0.0.1:9", "memberPasswordFile": "unused-file",
		"iam":            map[string]string{"Target": "127.0.0.1:9", "ServiceTokenFile": "unused-token-file"},
		"org":            map[string]string{"Target": "127.0.0.1:9", "ServiceTokenFile": "unused-token-file"},
		"sharedMessages": map[string]any{"listen": "127.0.0.1:0", "tenantId": uuid.NewString(), "allowedNodeIds": []string{uuid.NewString()}, "messagePublicKeys": map[string]string{}}}
	raw, err := json.Marshal(content)
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "runtime.json")
	if err = os.WriteFile(file, raw, 0600); err != nil {
		t.Fatal(err)
	}
	var out, logs bytes.Buffer
	err = run([]string{"--bifrost-config", file}, &out, &logs)
	if err == nil || strings.Contains(err.Error(), "unknown field") || !strings.Contains(err.Error(), "public keys") {
		t.Fatal("real main did not install the message runtime", err)
	}
}
