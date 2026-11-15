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
	want := "auriga.iam.clients[0].id=auriga-dsh\nauriga.iam.clients[0].tenant=00000000-0000-0000-0000-000000000001\nauriga.iam.clients[0].secret-hash=\nauriga.iam.clients[0].redirect-uris=\nauriga.iam.clients[0].scopes[0]=auriga.connect\nauriga.iam.clients[0].grants[0]=urn:ietf:params:oauth:grant-type:device_code\nauriga.iam.clients[0].grants[1]=refresh_token\nauriga.iam.clients[0].platform=true\nauriga.iam.clients[0].native-connect=false\nauriga.iam.clients[1].id=auriga-dsh-native\nauriga.iam.clients[1].tenant=00000000-0000-0000-0000-000000000001\nauriga.iam.clients[1].secret-hash=\nauriga.iam.clients[1].redirect-uris=\nauriga.iam.clients[1].scopes[0]=auriga.connect\nauriga.iam.clients[1].grants[0]=urn:ietf:params:oauth:grant-type:device_code\nauriga.iam.clients[1].grants[1]=refresh_token\nauriga.iam.clients[1].platform=true\nauriga.iam.clients[1].native-connect=true\n"
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
	for _, name := range []string{"missing", "tenant", "client", "secret", "redirect", "scope", "grant", "duplicate", "standard-mode", "native-mode"} {
		t.Run(name, func(t *testing.T) {
			changed := string(raw)
			switch name {
			case "standard-mode":
				changed = strings.ReplaceAll(changed, "clients[0].native-connect=false", "clients[0].native-connect=true")
			case "native-mode":
				changed = strings.ReplaceAll(changed, "clients[1].native-connect=true", "clients[1].native-connect=false")
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
	if composeRecipe(false) != "network-wss-v5-https-dual-client-bifrost-private-broker" || composeRecipe(true) != "network-wss-v8-https-dual-client-managed-device-panel-bifrost-private-broker" {
		t.Fatal("private Broker topology must use new sealed recipes for both access modes")
	}
}

func TestComposeRegistersBothPublicDeviceClients(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "docker-compose.yml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, registration := range []string{
		"AURIGA_IAM_CLIENTS_0_ID: auriga-dsh\n",
		"AURIGA_IAM_CLIENTS_0_NATIVECONNECT: 'false'",
		"AURIGA_IAM_CLIENTS_1_ID: auriga-dsh-native\n",
		"AURIGA_IAM_CLIENTS_1_NATIVECONNECT: 'true'",
	} {
		if !strings.Contains(string(raw), registration) {
			t.Fatalf("missing public client registration %q", registration)
		}
	}
}
