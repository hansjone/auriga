package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNativeClientRegistration(t *testing.T) {
	out := t.TempDir()
	if err := os.Mkdir(filepath.Join(out, "network-platform"), 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(out, "network-platform", "platform.properties")
	const original = "auriga.iam.ai-gateway-token-export.enabled=true\n"
	if err := os.WriteFile(path, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	if err := writeNativeClient(out); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil || !strings.HasPrefix(string(raw), original) {
		t.Fatal("native client changed existing issuer properties")
	}
	want := "auriga.iam.clients[0].id=auriga-dsh\nauriga.iam.clients[0].tenant=00000000-0000-0000-0000-000000000001\nauriga.iam.clients[0].secret-hash=\nauriga.iam.clients[0].redirect-uris=\nauriga.iam.clients[0].scopes[0]=auriga.connect\nauriga.iam.clients[0].grants[0]=urn:ietf:params:oauth:grant-type:device_code\nauriga.iam.clients[0].grants[1]=refresh_token\nauriga.iam.clients[0].platform=true\nauriga.iam.clients[0].native-connect=true\n"
	if string(raw) != original+want || checkNativeClient(out) != nil {
		t.Fatal("native public client does not match the fixed registration")
	}
	if err := writeNativeClient(out); err == nil {
		t.Fatal("duplicate native client appended")
	}
	after, _ := os.ReadFile(path)
	if string(after) != string(raw) {
		t.Fatal("duplicate registration changed properties")
	}
	for _, name := range []string{"missing", "tenant", "client", "secret", "redirect", "scope", "grant", "duplicate"} {
		t.Run(name, func(t *testing.T) {
			changed := string(raw)
			switch name {
			case "missing":
				changed = original
			case "tenant":
				changed = strings.ReplaceAll(changed, composeTenant, "00000000-0000-0000-0000-000000000002")
			case "client":
				changed += "auriga.iam.clients[1].id=other\n"
			case "secret":
				changed = strings.ReplaceAll(changed, "secret-hash=\n", "secret-hash=unexpected\n")
			case "redirect":
				changed = strings.ReplaceAll(changed, "redirect-uris=\n", "redirect-uris=https://example.com/callback\n")
			case "scope":
				changed += "auriga.iam.clients[0].scopes[1]=openid\n"
			case "grant":
				changed += "auriga.iam.clients[0].grants[2]=authorization_code\n"
			case "duplicate":
				changed += want
			}
			if err := os.WriteFile(path, []byte(changed), 0600); err != nil {
				t.Fatal(err)
			}
			if err := checkNativeClient(out); err == nil {
				t.Fatal("invalid native registration accepted")
			}
			after, _ := os.ReadFile(path)
			if string(after) != changed {
				t.Fatal("registration check repaired existing properties")
			}
		})
	}
}

func TestNativeRecipeCompatibility(t *testing.T) {
	if composeRecipe(false) != "network-wss-v2-https-litellm-ui-v2" || composeRecipe(true) != "network-wss-v3-https-native-client-litellm-ui-v2" {
		t.Fatal("HTTP legacy input changed or HTTPS native recipe not distinguished")
	}
}
