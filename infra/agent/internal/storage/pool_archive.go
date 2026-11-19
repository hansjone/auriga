package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	bolt "go.etcd.io/bbolt"
)

type archiveOperation struct {
	Source, Archive string
	Deleted         bool
}

func (l *PoolLayout) archivePath(ctx context.Context, pool, operation string) (string, error) {
	if backend, ok := l.Quota.(interface {
		ArchivePath(context.Context, string, string) (string, error)
	}); ok {
		return backend.ArchivePath(ctx, pool, operation)
	}
	return l.Path(pool, "control", "archives", operation)
}

// The node-private journal serializes reset and retention across processes.
// Persist the exact member identity BEFORE moving any data; retries may never
// borrow an existing archive or repeat an operation after retention deleted it.
func (l *PoolLayout) withArchive(pool string, fn func(*bolt.DB) error) error {
	p, err := l.Path(pool, "control", "archive-operations")
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		return err
	}
	db, err := bolt.Open(p, 0600, &bolt.Options{Timeout: 10 * time.Second})
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	return fn(db)
}

var archiveBucket = []byte("pool-archive-operations-v1")

func archiveRecord(db *bolt.DB, operation string, expected *archiveOperation) (*archiveOperation, error) {
	var result archiveOperation
	err := db.Update(func(tx *bolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists(archiveBucket)
		if err != nil {
			return err
		}
		if raw := b.Get([]byte(operation)); raw != nil {
			if json.Unmarshal(raw, &result) != nil || result.Archive == "" {
				return fmt.Errorf("invalid archive journal")
			}
			if expected != nil && (result.Source != expected.Source || result.Archive != expected.Archive) {
				return fmt.Errorf("archive operation identity changed")
			}
			return nil
		}
		if expected == nil {
			return fmt.Errorf("archive operation not found")
		}
		if _, err := os.Lstat(expected.Archive); !os.IsNotExist(err) {
			return fmt.Errorf("unjournaled archive already exists")
		}
		result = *expected
		raw, err := json.Marshal(result)
		if err != nil {
			return err
		}
		return b.Put([]byte(operation), raw)
	})
	return &result, err
}

// ArchiveReset never crosses the ext4 mount boundary. A committed reservation
// precedes rename; an interrupted move/quota/reset can resume without copying,
// overwriting or discarding member data. Caller first quiesces this member.
func (l *PoolLayout) ArchiveReset(ctx context.Context, pool, tenant, member string, epoch int64, operation string) (string, error) {
	p, err := l.Workspace(pool, tenant, member, epoch)
	if err != nil {
		return "", err
	}
	a, err := l.archivePath(ctx, pool, operation)
	if err != nil {
		return "", err
	}
	if l.Quota == nil {
		return "", fmt.Errorf("hard quota unavailable")
	}
	err = l.withArchive(pool, func(db *bolt.DB) error {
		state, err := archiveRecord(db, operation, &archiveOperation{Source: p, Archive: a})
		if err != nil {
			return err
		}
		if state.Deleted {
			return fmt.Errorf("archive operation already deleted")
		}
		if err = os.MkdirAll(filepath.Dir(a), 0700); err != nil {
			return err
		}
		if err = os.Chmod(filepath.Dir(a), 0700); err != nil {
			return err
		}
		if err = Lchown(filepath.Dir(a), os.Getuid(), os.Getgid()); err != nil {
			return err
		}
		if _, err = os.Lstat(a); os.IsNotExist(err) {
			if err = os.Rename(p, a); err != nil {
				return err
			}
		} else if err != nil {
			return err
		}
		// Sync both parents on every retry, including recovery after rename.
		if err = syncArchiveDirectory(filepath.Dir(a)); err != nil {
			return err
		}
		if err = syncArchiveDirectory(filepath.Dir(p)); err != nil {
			return err
		}
		if err = l.Quota.Ensure(ctx, a, MemberBytes, MemberInodes); err != nil {
			return err
		}
		_, err = l.Prepare(ctx, pool, tenant, member, epoch)
		return err
	})
	return a, err
}

// DeleteArchive accepts identifiers, never an untrusted absolute path. A durable
// tombstone blocks resets before deletion begins, even across a crash.
func (l *PoolLayout) DeleteArchive(ctx context.Context, pool, operation string) error {
	p, err := l.archivePath(ctx, pool, operation)
	if err != nil {
		return err
	}
	return l.withArchive(pool, func(db *bolt.DB) error {
		state, err := archiveRecord(db, operation, nil)
		if err != nil {
			return err
		}
		if state.Archive != p {
			return fmt.Errorf("archive backend changed")
		}
		state.Deleted = true
		if err = db.Update(func(tx *bolt.Tx) error {
			raw, err := json.Marshal(state)
			if err != nil {
				return err
			}
			return tx.Bucket(archiveBucket).Put([]byte(operation), raw)
		}); err != nil {
			return err
		}
		if err = os.RemoveAll(p); err != nil {
			return err
		}
		if err = syncArchiveDirectory(filepath.Dir(p)); err != nil {
			return err
		}
		if quota, ok := l.Quota.(interface {
			Release(context.Context, string) error
		}); ok {
			return quota.Release(ctx, p)
		}
		return nil
	})
}
