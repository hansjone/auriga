package bifrostbackend

import (
	"encoding/json"
	"testing"
)

func TestAllowPrivateProvidersDefaultTrue(t *testing.T) {
	var omitted Config
	if err := json.Unmarshal([]byte(`{}`), &omitted); err != nil || !omitted.allowPrivateProviders() {
		t.Fatalf("omitted=%v err=%v", omitted.allowPrivateProviders(), err)
	}
	off := false
	on := true
	if (Config{AllowPrivateProviders: &off}).allowPrivateProviders() {
		t.Fatal("explicit false")
	}
	if !(Config{AllowPrivateProviders: &on}).allowPrivateProviders() {
		t.Fatal("explicit true")
	}
}
