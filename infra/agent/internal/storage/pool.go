package storage

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strconv"
)

const MemberBytes int64 = 5368709120
const MemberInodes int64 = 100000

// Twenty member workspaces, one persistent Host home and one message trust
// ledger. Message preferences and inboxes share the Host home's quota.
const PoolMinimumBytes int64 = 22 * MemberBytes

// HardQuota must install and verify a kernel-enforced quota before publication.
// Directory size scans are not an implementation of this interface.
type HardQuota interface {
	Ensure(context.Context, string, int64, int64) error
}

// PoolLayout is node-owned. Root defaults to /var/lib/auriga/pools in production;
// it and control must never be writable or mounted by the DSH process.
type PoolLayout struct {
	Root  string
	Quota HardQuota
}

var poolComponent = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$`)

func (l *PoolLayout) Path(pool string, parts ...string) (string, error) {
	for _, v := range append([]string{pool}, parts...) {
		if !poolComponent.MatchString(v) {
			return "", fmt.Errorf("invalid pool path component")
		}
	}
	return SafeJoin(l.Root, append([]string{pool}, parts...)...)
}
func (l *PoolLayout) Workspace(pool, tenant, member string, epoch int64) (string, error) {
	if epoch < 1 || epoch > 9007199254740991 {
		return "", fmt.Errorf("invalid epoch")
	}
	return l.Path(pool, "members", tenant, member, strconv.FormatInt(epoch, 10), "workspace")
}
func (l *PoolLayout) Prepare(ctx context.Context, pool, tenant, member string, epoch int64) (string, error) {
	if backend, ok := l.Quota.(interface {
		MountPool(context.Context, string) (PoolMount, error)
	}); ok {
		if _, err := backend.MountPool(ctx, pool); err != nil {
			return "", err
		}
	}
	p, err := l.Workspace(pool, tenant, member, epoch)
	if err != nil {
		return "", err
	}
	if l.Quota == nil {
		return "", fmt.Errorf("hard quota unavailable")
	}
	if err = os.MkdirAll(p, 0700); err != nil {
		return "", err
	}
	if err = l.Quota.Ensure(ctx, p, MemberBytes, MemberInodes); err != nil {
		return "", err
	}
	entries, err := os.ReadDir(p)
	if err != nil {
		return "", err
	}
	if len(entries) != 0 {
		return "", fmt.Errorf("workspace is not empty")
	}
	return p, nil
}

// RetireWorkspace never recursively deletes member data: W must first archive
// and quiesce this member. A missing/empty old root can then release its quota.
func (l *PoolLayout) RetireWorkspace(ctx context.Context, pool, tenant, member string, epoch int64) error {
	p, err := l.Workspace(pool, tenant, member, epoch)
	if err != nil {
		return err
	}
	entries, err := os.ReadDir(p)
	if err == nil {
		if len(entries) != 0 {
			return fmt.Errorf("member workspace not archived")
		}
		if err = os.Remove(p); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	if quota, ok := l.Quota.(interface {
		Release(context.Context, string) error
	}); ok {
		return quota.Release(ctx, p)
	}
	return nil
}
