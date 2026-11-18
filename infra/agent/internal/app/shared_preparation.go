package app

import (
	"fmt"
	"path/filepath"
	"runtime"

	"github.com/hansjone/auriga/infra/agent/internal/config"
	agentruntime "github.com/hansjone/auriga/infra/agent/internal/runtime"
	"github.com/hansjone/auriga/infra/agent/internal/storage"
)

// The node's existing state.Open lock owns DataRoot for the entire run.
// This adapter installs the real XFS quota boundary and advertises preparation only.
func sharedPreparation(cfg config.Config, driver string) (*agentruntime.SharedPools, error) {
	provision := cfg.SharedDshPreparation
	if provision == nil {
		return nil, nil
	}
	if runtime.GOOS != "linux" || driver != "docker" || !filepath.IsAbs(cfg.DataRoot) ||
		provision.ProjectFirst == 0 || provision.ProjectLast < provision.ProjectFirst {
		return nil, fmt.Errorf("shared preparation requires Linux Docker and absolute XFS provisioning with a reserved project range")
	}
	root := filepath.Join(cfg.DataRoot, "pools")
	if err := storage.CheckNoSymlink(cfg.DataRoot, root); err != nil {
		return nil, err
	}
	ids := &storage.ProjectIDs{File: filepath.Join(cfg.AgentDir(), "pool-projects.db"),
		First: provision.ProjectFirst, Last: provision.ProjectLast}
	var quota storage.HardQuota
	switch provision.Backend {
	case "ext4-image":
		if !filepath.IsAbs(provision.MkfsBinary) || !filepath.IsAbs(provision.LosetupBinary) || provision.ImageBytes < 21*storage.MemberBytes {
			return nil, fmt.Errorf("ext4 image size and absolute storage tools required")
		}
		quota = &storage.Ext4Images{Root: root, IDs: ids, ImageBytes: provision.ImageBytes,
			MkfsBinary: provision.MkfsBinary, LosetupBinary: provision.LosetupBinary}
	case "", "xfs":
		if !filepath.IsAbs(provision.QuotaBinary) || !filepath.IsAbs(provision.QuotaMount) {
			return nil, fmt.Errorf("absolute XFS provisioning required")
		}
		if err := storage.AssertUnderRoot(provision.QuotaMount, root); err != nil {
			return nil, err
		}
		quota = &storage.XFSProjectQuota{Binary: provision.QuotaBinary, Mount: provision.QuotaMount, IDs: ids}
	default:
		return nil, fmt.Errorf("unknown shared storage backend")
	}
	return &agentruntime.SharedPools{Layout: &storage.PoolLayout{Root: root, Quota: quota}}, nil
}
