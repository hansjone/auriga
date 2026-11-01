package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/hansjone/auriga/infra/gen/deployment"
)

func TestLiteLLMLoginEnvironmentPreservesUsernameAndReader(t *testing.T) {
	cfg, _, _, _, _, _ := testEnv(t)
	cfg.LiteLLMOrigin = "http://127.0.0.1:4000"
	cfg.LiteLLMUI = &LiteLLMUIConfig{
		Origin:   deployment.Origin{Scheme: "http", Host: "192.0.2.10", Port: 8086},
		Listener: deployment.Listener{Address: "0.0.0.0", Port: 8086},
		Login:    &LiteLLMUILogin{Username: "custom-user", PasswordFile: "old", KeyInfoKeyFile: "reader"},
	}
	accessFile := "../../contracts/deployment/fixtures/ip-http.json"
	file := filepath.Join(t.TempDir(), "config.json")
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(file, raw, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AURIGA_EDGE_LITELLM_UI_PASSWORD_FILE", "new-password")
	t.Setenv("AURIGA_EDGE_STATIC_GZIP_CACHE_BYTES", "4096")
	loaded, err := loadConfigWithAccess(file, accessFile)
	if err != nil {
		t.Fatal(err)
	}
	login := loaded.LiteLLMUI.Login
	if login.Username != "custom-user" || login.KeyInfoKeyFile != "reader" || login.PasswordFile != "new-password" || loaded.Static.GzipCacheBytes != 4096 {
		t.Fatal("environment discarded explicit login fields")
	}
	t.Setenv("AURIGA_EDGE_STATIC_GZIP_CACHE_BYTES", "invalid")
	if _, err = loadConfigWithAccess(file, accessFile); err == nil {
		t.Fatal("invalid budget accepted")
	}
}
