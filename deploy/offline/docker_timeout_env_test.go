package main

import "testing"

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
