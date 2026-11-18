package app

import (
	"path/filepath"
	"runtime"
	"testing"

	"github.com/hansjone/auriga/infra/agent/internal/config"
	"github.com/hansjone/auriga/infra/agent/internal/storage"
)

func TestSharedPreparationDefaultsAndProvisioning(t *testing.T) {
	cfg := config.Default()
	if pools, err := sharedPreparation(cfg, "docker"); err != nil || pools != nil {
		t.Fatal("default enabled shared preparation")
	}
	cfg.DataRoot = t.TempDir()
	cfg.SharedDshPreparation = &config.SharedDshPreparation{}
	if _, err := sharedPreparation(cfg, "docker"); err == nil {
		t.Fatal("incomplete provisioning accepted")
	}
	if _, err := sharedPreparation(cfg, "fake"); err == nil {
		t.Fatal("fake runtime accepted")
	}
	if runtime.GOOS != "linux" {
		return
	}
	cfg.SharedDshPreparation = &config.SharedDshPreparation{
		QuotaBinary: "/usr/sbin/xfs_quota", QuotaMount: cfg.DataRoot, ProjectFirst: 10000, ProjectLast: 11000,
	}
	pools, err := sharedPreparation(cfg, "docker")
	if err != nil {
		t.Fatal(err)
	}
	if pools.Layout.Root != filepath.Join(cfg.DataRoot, "pools") || pools.Sessions != nil || pools.Supervisor != nil {
		t.Fatal("unexpected pool ownership or lifecycle adapter")
	}
	if _, ok := pools.Layout.Quota.(*storage.XFSProjectQuota); !ok {
		t.Fatal("missing real XFS quota adapter")
	}
	// Constructing the adapter neither invokes XFS nor creates a member workspace.
}
