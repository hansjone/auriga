package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// This public M2 client carries no credential or redirect capability.
const nativeClientProperties = "auriga.iam.clients[0].id=auriga-dsh\n" +
	"auriga.iam.clients[0].tenant=" + composeTenant + "\n" +
	"auriga.iam.clients[0].secret-hash=\n" +
	"auriga.iam.clients[0].redirect-uris=\n" +
	"auriga.iam.clients[0].scopes[0]=auriga.connect\n" +
	"auriga.iam.clients[0].grants[0]=urn:ietf:params:oauth:grant-type:device_code\n" +
	"auriga.iam.clients[0].grants[1]=refresh_token\n" +
	"auriga.iam.clients[0].platform=true\n" +
	"auriga.iam.clients[0].native-connect=true\n"

func composeRecipe(https bool) string {
	if https {
		return "network-wss-v3-https-native-client-litellm-v1"
	}
	return "network-wss-v2-https-litellm-v1"
}

func nativeClientSection(raw []byte) string {
	var section strings.Builder
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "auriga.iam.clients") {
			section.WriteString(line + "\n")
		}
	}
	return section.String()
}

func writeNativeClient(out string) error {
	path := filepath.Join(out, "network-platform", "platform.properties")
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if nativeClientSection(raw) != "" {
		return fmt.Errorf("native client registration already present")
	}
	return os.WriteFile(path, append(raw, []byte(nativeClientProperties)...), 0600)
}

// Call only after verifying the sealed base; never repair an old installation.
func checkNativeClient(out string) error {
	raw, err := os.ReadFile(filepath.Join(out, "network-platform", "platform.properties"))
	if err != nil || nativeClientSection(raw) != nativeClientProperties {
		return fmt.Errorf("requires sealed native client registration; no automatic migration")
	}
	return nil
}
