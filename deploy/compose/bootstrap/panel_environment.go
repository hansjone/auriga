package main

import (
	"bytes"
	"strings"
)

// Rotation still compares every byte with trusted inputs. Only the key's real
// UID/GID may extend the older sealed environment; caller-selected users cannot.
func matchesPanelComposeEnvironment(out string, keys, ca, access, env []byte) bool {
	prefix := append(append([]byte{}, keys...), []byte("AURIGA_AGENT_CA_SHA256="+strings.TrimSpace(string(ca))+"\n")...)
	defer clear(prefix)
	legacy := append(append([]byte{}, prefix...), access...)
	defer clear(legacy)
	if bytes.Equal(env, legacy) {
		return true
	}
	owner, err := panelOwnerEnvironment(out)
	if err != nil {
		return false
	}
	current := append(append(append([]byte{}, prefix...), owner...), access...)
	defer clear(current)
	return bytes.Equal(env, current)
}
