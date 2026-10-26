//go:build !windows

package relayhttp

import "testing"

func protectKey(t *testing.T, _ string) { t.Helper() } // os.WriteFile uses 0600.
