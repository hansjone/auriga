package storage

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// PoolMount is a node-owned filesystem which the Engine mounts in its own namespace.
type PoolMount struct {
	Device string
	Root   string
}

// Ext4Images keeps each pool on a preallocated file, with kernel project quotas.
// The backing NAS filesystem needs regular files and loop-device support.
type Ext4Images struct {
	Root, MkfsBinary, LosetupBinary string
	ImageBytes                      int64
	IDs                             *ProjectIDs
	mu                              sync.Mutex
	mount                           func(context.Context, string, string, int64) (string, error)
	quota                           func(string, string, uint32, int64, int64) error
}

func (s *Ext4Images) MountPool(ctx context.Context, pool string) (PoolMount, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !filepath.IsAbs(s.Root) || s.ImageBytes < 21*MemberBytes || s.ImageBytes%4096 != 0 || s.IDs == nil {
		return PoolMount{}, fmt.Errorf("invalid ext4 pool storage provisioning")
	}
	layout := &PoolLayout{Root: s.Root}
	image, err := layout.Path(pool, "control", "members-ext4")
	if err != nil {
		return PoolMount{}, err
	}
	root, err := layout.Path(pool, "members")
	if err != nil {
		return PoolMount{}, err
	}
	if err := os.MkdirAll(filepath.Dir(image), 0700); err != nil {
		return PoolMount{}, err
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		return PoolMount{}, err
	}
	mount := s.mount
	if mount == nil {
		mount = func(ctx context.Context, image, root string, size int64) (string, error) {
			return mountExt4Image(ctx, image, root, size, s.MkfsBinary, s.LosetupBinary)
		}
	}
	device, err := mount(ctx, image, root, s.ImageBytes)
	if err != nil {
		return PoolMount{}, err
	}
	return PoolMount{Device: device, Root: root}, nil
}

func (s *Ext4Images) Ensure(ctx context.Context, path string, bytes, inodes int64) error {
	if bytes != MemberBytes || inodes != MemberInodes || s.IDs == nil {
		return fmt.Errorf("unexpected shared member quota")
	}
	if err := CheckNoSymlink(s.Root, path); err != nil {
		return err
	}
	rel, err := filepath.Rel(s.Root, path)
	if err != nil {
		return err
	}
	parts := strings.Split(rel, string(filepath.Separator))
	if len(parts) < 3 || parts[1] != "members" {
		return fmt.Errorf("ext4 quota path must belong to the pool member filesystem")
	}
	fs, err := s.MountPool(ctx, parts[0])
	if err != nil {
		return err
	}
	quota := s.quota
	if quota == nil {
		quota = setExt4ProjectQuota
	}
	return s.IDs.Use(path, func(id uint32) error { return quota(fs.Device, path, id, bytes, inodes) })
}
