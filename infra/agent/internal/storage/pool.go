package storage

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
)

const MemberBytes int64 = 5368709120
const MemberInodes int64 = 100000

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

// ArchiveReset is retriable after either rename or quota failure. archive is
// node-private, never an input to a new session. Caller first confirms pool stop.
func (l *PoolLayout) ArchiveReset(ctx context.Context, pool, tenant, member string, epoch int64, operation string) (string, error) {
	p, err := l.Workspace(pool, tenant, member, epoch)
	if err != nil {
		return "", err
	}
	a, err := l.Path(pool, "control", "archives", operation)
	if err != nil {
		return "", err
	}
	if err = os.MkdirAll(filepath.Dir(a), 0700); err != nil {
		return "", err
	}
	if _, err = os.Lstat(a); os.IsNotExist(err) {
		if err = os.Rename(p, a); err != nil {
			return "", err
		}
	} else if err != nil {
		return "", err
	}
	// Give the retained archive its own project before reusing the active quota.
	if l.Quota == nil {
		return "", fmt.Errorf("hard quota unavailable")
	}
	if err = l.Quota.Ensure(ctx, a, MemberBytes, MemberInodes); err != nil {
		return "", err
	}
	_, err = l.Prepare(ctx, pool, tenant, member, epoch)
	return a, err
}

// DeleteArchive accepts identifiers, never caller-supplied absolute paths.
func (l *PoolLayout) DeleteArchive(pool, operation string) error {
	p, err := l.Path(pool, "control", "archives", operation)
	if err != nil {
		return err
	}
	return os.RemoveAll(p)
}
