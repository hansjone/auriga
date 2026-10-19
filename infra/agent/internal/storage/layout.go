// Package storage manages instance directory layout, soft quotas, and schema migrations.
package storage

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	schemaFileName = "schema-version"
	// CurrentSchema is the newest directory layout version this agent understands.
	CurrentSchema uint32 = 1
)

// Layout creates and migrates instance directories under <data>/instances/<id>/.
type Layout struct {
	InstancesDir string
	// Chown, when set, changes ownership of instance dirs to the instance host uid/gid.
	// On systems without privilege this may be a no-op stub.
	Chown func(path string, uid, gid int) error
}

// Paths are the subdirectories of one instance.
type Paths struct {
	Root  string
	Home  string
	Meta  string
	Trash string
}

// For returns paths for instanceID without creating them.
func (l *Layout) For(instanceID string) (Paths, error) {
	if err := validateInstanceID(instanceID); err != nil {
		return Paths{}, err
	}
	root := filepath.Join(l.InstancesDir, instanceID)
	return Paths{
		Root:  root,
		Home:  filepath.Join(root, "home"),
		Meta:  filepath.Join(root, "meta"),
		Trash: filepath.Join(root, "trash"),
	}, nil
}

// Ensure creates the §8.1 layout and home subdirs. uid/gid are the instance user (0 = skip chown).
func (l *Layout) Ensure(instanceID string, uid, gid int) (Paths, error) {
	p, err := l.For(instanceID)
	if err != nil {
		return Paths{}, err
	}
	for _, dir := range []string{
		p.Root, p.Home, p.Meta, p.Trash,
		filepath.Join(p.Home, ".dsh"),
		filepath.Join(p.Home, ".memwiki"),
		filepath.Join(p.Home, ".auriga"),
		filepath.Join(p.Home, "workspace"),
	} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return Paths{}, fmt.Errorf("storage: mkdir %s: %w", dir, err)
		}
	}
	schemaPath := filepath.Join(p.Meta, schemaFileName)
	if _, err := os.Stat(schemaPath); os.IsNotExist(err) {
		if err := os.WriteFile(schemaPath, []byte("1\n"), 0o644); err != nil {
			return Paths{}, err
		}
	}
	if uid > 0 && l.Chown != nil {
		for _, dir := range []string{p.Root, p.Home, p.Meta, p.Trash} {
			if err := l.Chown(dir, uid, gid); err != nil {
				return Paths{}, fmt.Errorf("storage: chown %s: %w", dir, err)
			}
		}
	}
	return p, nil
}

// SchemaVersion reads meta/schema-version (0 if missing).
func (l *Layout) SchemaVersion(instanceID string) (uint32, error) {
	p, err := l.For(instanceID)
	if err != nil {
		return 0, err
	}
	raw, err := os.ReadFile(filepath.Join(p.Meta, schemaFileName))
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	n, err := strconv.ParseUint(strings.TrimSpace(string(raw)), 10, 32)
	if err != nil {
		return 0, fmt.Errorf("storage: schema-version: %w", err)
	}
	return uint32(n), nil
}

// Migrate runs idempotent steps until local schema >= want.
// Migrations are local-only (no network / other hosts).
func (l *Layout) Migrate(instanceID string, want uint32) error {
	if want == 0 {
		return nil
	}
	p, err := l.Ensure(instanceID, 0, 0)
	if err != nil {
		return err
	}
	cur, err := l.SchemaVersion(instanceID)
	if err != nil {
		return err
	}
	for cur < want {
		next := cur + 1
		if next > CurrentSchema && next > want {
			return fmt.Errorf("storage: unknown schema version %d", next)
		}
		switch next {
		case 1:
			// v1 is the initial layout; Ensure already created it.
		default:
			// Future steps go here; unknown versions above CurrentSchema fail.
			if next > CurrentSchema {
				return fmt.Errorf("storage: cannot migrate to schema %d (agent supports %d)", want, CurrentSchema)
			}
		}
		if err := os.WriteFile(filepath.Join(p.Meta, schemaFileName), []byte(fmt.Sprintf("%d\n", next)), 0o644); err != nil {
			return err
		}
		cur = next
	}
	return nil
}

// Remove deletes the instance directory tree (local only).
func (l *Layout) Remove(instanceID string) error {
	p, err := l.For(instanceID)
	if err != nil {
		return err
	}
	return os.RemoveAll(p.Root)
}

// DiskUsageMB walks the instance tree and returns approximate used mebibytes.
func (l *Layout) DiskUsageMB(instanceID string) (int64, error) {
	p, err := l.For(instanceID)
	if err != nil {
		return 0, err
	}
	var total int64
	err = filepath.Walk(p.Root, func(_ string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			total += info.Size()
		}
		return nil
	})
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	return (total + (1<<20 - 1)) >> 20, nil
}

// SoftQuotaExceeded reports whether usage exceeds diskGB (soft limit when project quotas unavailable).
func (l *Layout) SoftQuotaExceeded(instanceID string, diskGB int64) (bool, int64, error) {
	if diskGB <= 0 {
		return false, 0, nil
	}
	used, err := l.DiskUsageMB(instanceID)
	if err != nil {
		return false, 0, err
	}
	limitMB := diskGB * 1024
	return used > limitMB, used, nil
}

func validateInstanceID(id string) error {
	if id == "" {
		return fmt.Errorf("storage: empty instance id")
	}
	if strings.Contains(id, "..") || strings.ContainsAny(id, `/\`) {
		return fmt.Errorf("storage: invalid instance id")
	}
	return nil
}
