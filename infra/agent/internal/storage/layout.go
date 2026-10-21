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
	// Chown, when set, changes ownership of instance dirs to the instance host uid/gid
	// without following symlinks (Lchown).
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
	if err := ValidateInstanceID(instanceID); err != nil {
		return Paths{}, err
	}
	var p Paths
	var err error
	if p.Root, err = SafeJoin(l.InstancesDir, instanceID); err != nil {
		return Paths{}, err
	}
	if p.Home, err = SafeJoin(l.InstancesDir, instanceID, "home"); err != nil {
		return Paths{}, err
	}
	if p.Meta, err = SafeJoin(l.InstancesDir, instanceID, "meta"); err != nil {
		return Paths{}, err
	}
	if p.Trash, err = SafeJoin(l.InstancesDir, instanceID, "trash"); err != nil {
		return Paths{}, err
	}
	return p, nil
}

// Ensure creates the §8.1 layout and home subdirs. uid/gid are the instance user (0 = skip chown).
func (l *Layout) Ensure(instanceID string, uid, gid int) (Paths, error) {
	p, err := l.For(instanceID)
	if err != nil {
		return Paths{}, err
	}
	dirs := []string{
		p.Root, p.Home, p.Meta, p.Trash,
		filepath.Join(p.Home, ".dsh"),
		filepath.Join(p.Home, ".memwiki"),
		filepath.Join(p.Home, ".auriga"),
		filepath.Join(p.Home, "workspace"),
	}
	for _, dir := range dirs {
		// home/* is writable by the instance: refuse to follow a symlink planted
		// there (before mkdir, and again after to catch a swap in between).
		if err := CheckNoSymlink(l.InstancesDir, dir); err != nil {
			return Paths{}, err
		}
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return Paths{}, fmt.Errorf("storage: mkdir %s: %w", dir, err)
		}
		if err := CheckNoSymlink(l.InstancesDir, dir); err != nil {
			return Paths{}, err
		}
	}
	schemaPath, err := SafeJoin(l.InstancesDir, instanceID, "meta", schemaFileName)
	if err != nil {
		return Paths{}, err
	}
	if _, err := os.Stat(schemaPath); os.IsNotExist(err) {
		if err := os.WriteFile(schemaPath, []byte("1\n"), 0o644); err != nil {
			return Paths{}, err
		}
	}
	if uid > 0 && l.Chown != nil {
		for _, dir := range dirs {
			if err := l.Chown(dir, uid, gid); err != nil {
				return Paths{}, fmt.Errorf("storage: chown %s: %w", dir, err)
			}
		}
	}
	return p, nil
}

// SchemaVersion reads meta/schema-version (0 if missing).
func (l *Layout) SchemaVersion(instanceID string) (uint32, error) {
	if _, err := l.For(instanceID); err != nil {
		return 0, err
	}
	schemaPath, err := SafeJoin(l.InstancesDir, instanceID, "meta", schemaFileName)
	if err != nil {
		return 0, err
	}
	raw, err := os.ReadFile(schemaPath)
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
func (l *Layout) Migrate(instanceID string, want uint32) error {
	if want == 0 {
		return nil
	}
	if _, err := l.Ensure(instanceID, 0, 0); err != nil {
		return err
	}
	cur, err := l.SchemaVersion(instanceID)
	if err != nil {
		return err
	}
	for cur < want {
		next := cur + 1
		switch next {
		case 1:
			// v1 is the initial layout; Ensure already created it.
		default:
			if next > CurrentSchema {
				return fmt.Errorf("storage: cannot migrate to schema %d (agent supports %d)", want, CurrentSchema)
			}
		}
		schemaPath, err := SafeJoin(l.InstancesDir, instanceID, "meta", schemaFileName)
		if err != nil {
			return err
		}
		if err := os.WriteFile(schemaPath, []byte(fmt.Sprintf("%d\n", next)), 0o644); err != nil {
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

// SoftQuotaExceeded reports whether usage exceeds diskGB.
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
