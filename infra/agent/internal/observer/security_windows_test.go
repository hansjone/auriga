//go:build windows

package observer

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hansjone/auriga/infra/agent/internal/ingress"
)

func TestSchedule_RejectsJunction(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "store.json"), []byte(`{"jobs":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", filepath.Join(root, "ops-cron"), outside).CombinedOutput(); err != nil {
		t.Fatalf("mklink: %v %s", err, out)
	}
	if _, err := readSchedule(root, "ops-cron/store.json"); err == nil || !strings.Contains(err.Error(), ingress.CodePathEscape) {
		t.Fatalf("junction: %v", err)
	}
	if scheduleFilesChanged(root, "i1", map[string]time.Time{}) {
		t.Fatal("junction watched")
	}
}
