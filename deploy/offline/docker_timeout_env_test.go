package main

import (
	"testing"
	"time"
)

func TestValidateDockerImageTimeoutEnv(t *testing.T) {
	look := func(m map[string]string) func(string) (string, bool) {
		return func(k string) (string, bool) { v, ok := m[k]; return v, ok }
	}
	if err := validateDockerImageTimeoutEnv(look(nil)); err != nil {
		t.Fatal(err)
	}
	if err := validateDockerImageTimeoutEnv(look(map[string]string{"AURIGA_DOCKER_IMAGE_SAVE_MAX": "30m"})); err != nil {
		t.Fatal(err)
	}
	for _, v := range []string{"abc", "-5m", "0", "30s", "25h"} {
		if err := validateDockerImageTimeoutEnv(look(map[string]string{"AURIGA_DOCKER_INSPECT_TIMEOUT": v})); err == nil {
			t.Fatalf("应拒绝 %q", v)
		}
	}
}

func TestImageResolveLimitCoversBootstrapLimits(t *testing.T) {
	env := map[string]string{}
	lookup := func(k string) (string, bool) { v, ok := env[k]; return v, ok }
	if got := imageResolveLimit(lookup); got != 2*time.Hour+20*time.Minute {
		t.Fatalf("默认外层上限: %s", got)
	}
	env["AURIGA_DOCKER_IMAGE_SAVE_MAX"] = "5h"
	env["AURIGA_DOCKER_INSPECT_TIMEOUT"] = "30m"
	if got := imageResolveLimit(lookup); got != 5*time.Hour+40*time.Minute {
		t.Fatalf("调大后外层上限: %s", got)
	}
}
