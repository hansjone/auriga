package runtime_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	agentruntime "github.com/hansjone/auriga/infra/agent/internal/runtime"
	"github.com/hansjone/auriga/infra/agent/internal/storage"
)

// Record the real PersistentPools caller's distinct quota roots. This is not a
// replacement for the kernel quota implementation or an EDQUOT attestation.
type poolBudgetQuota struct {
	storage.HardQuota
	roots    map[string]bool
	failPath string
}

func (q *poolBudgetQuota) Ensure(ctx context.Context, path string, bytes, inodes int64) error {
	if path == q.failPath {
		return errors.New("synthetic ancestor quota unavailable")
	}
	if err := q.HardQuota.Ensure(ctx, path, bytes, inodes); err != nil {
		return err
	}
	q.roots[path] = true
	return nil
}

func TestPersistentPoolTwentyMembersUseTwentyTwoQuotaRoots(t *testing.T) {
	m, engine, quota := persistentFixture(t)
	budget := &poolBudgetQuota{HardQuota: quota, roots: map[string]bool{}}
	m.Pools.Layout.Quota = budget
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool := persistentIntent(1).Binding.PhysicalInstanceId
	members := filepath.Join(m.Pools.Layout.Root, pool, "members")
	home := filepath.Join(members, ".host")
	owned := map[string]bool{}
	chown := m.Chown
	m.Chown = func(path string, uid, gid int) error {
		if filepath.Dir(path) == home && !budget.roots[home] {
			t.Fatal("Host message directory published before its ancestor quota")
		}
		owned[path] = true
		return chown(path, uid, gid)
	}
	for i := 0; i < 20; i++ {
		command := persistentIntent(1)
		id := fmt.Sprintf("00000000-0000-4000-8000-%012d", i+100)
		command.OperationId, command.Binding.MemberId, command.Binding.PlacementId = id, id, id
		if err := m.View(ctx, persistentView(command)); err != nil {
			t.Fatal(err)
		}
		if receipt, err := m.Prepare(ctx, command); err != nil || receipt.Outcome != "confirmed" {
			t.Fatalf("member %d preparation: %v", i+1, err)
		}
	}
	if len(budget.roots) != 22 || !budget.roots[home] || !budget.roots[filepath.Join(members, "message-trust")] {
		t.Fatalf("20 members must share 22 quota roots, got %d: %v", len(budget.roots), budget.roots)
	}
	for _, name := range []string{"message-preferences", "message-inboxes"} {
		path := filepath.Join(home, name)
		info, err := os.Stat(path)
		if err != nil || !info.IsDir() || !owned[path] {
			t.Fatalf("Host quota descendant missing ownership: %s %v", path, err)
		}
		if runtime.GOOS != "windows" && info.Mode().Perm() != 0700 {
			t.Fatal("Host message directory permissions changed", info.Mode())
		}
		if budget.roots[path] {
			t.Fatal("message child acquired an extra quota project")
		}
		if _, err := os.Stat(filepath.Join(members, name)); !os.IsNotExist(err) {
			t.Fatal("new pool provisioned obsolete unshared message root", name, err)
		}
	}
	engine.mu.Lock()
	defer engine.mu.Unlock()
	if engine.creates != 1 || len(engine.starts) != 1 {
		t.Fatal("joining members replaced the pool process")
	}
}

func TestPersistentPoolMessageQuotaFailureNeverPublishes(t *testing.T) {
	for _, scenario := range []string{"home-quota", "trust-quota", "preferences-file", "inboxes-ownership"} {
		t.Run(scenario, func(t *testing.T) {
			m, engine, quota := persistentFixture(t)
			command := persistentIntent(1)
			members := filepath.Join(m.Pools.Layout.Root, command.Binding.PhysicalInstanceId, "members")
			home := filepath.Join(members, ".host")
			budget := &poolBudgetQuota{HardQuota: quota, roots: map[string]bool{}}
			m.Pools.Layout.Quota = budget
			published := false
			m.Publish = func(string, agentruntime.Endpoint) { published = true }
			switch scenario {
			case "home-quota":
				budget.failPath = home
			case "trust-quota":
				budget.failPath = filepath.Join(members, "message-trust")
			case "preferences-file":
				if err := os.MkdirAll(home, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(home, "message-preferences"), []byte("retained conflicting entry"), 0600); err != nil {
					t.Fatal(err)
				}
			case "inboxes-ownership":
				chown := m.Chown
				m.Chown = func(path string, uid, gid int) error {
					if path == filepath.Join(home, "message-inboxes") {
						return errors.New("synthetic Host ownership denied")
					}
					return chown(path, uid, gid)
				}
			}
			if err := m.View(context.Background(), persistentView(command)); err != nil {
				t.Fatal(err)
			}
			if receipt, err := m.Prepare(context.Background(), command); err == nil || receipt == nil || receipt.Outcome != "unconfirmed" {
				t.Fatal("unconfirmed Host storage admitted the pool", err)
			}
			if scenario == "preferences-file" {
				if raw, err := os.ReadFile(filepath.Join(home, "message-preferences")); err != nil || string(raw) != "retained conflicting entry" {
					t.Fatal("Node removed or overwrote a conflicting message entry", err)
				}
			}
			engine.mu.Lock()
			defer engine.mu.Unlock()
			if published || engine.creates != 0 || len(engine.starts) != 0 {
				t.Fatal("Host storage failure reached process publication")
			}
		})
	}
}
