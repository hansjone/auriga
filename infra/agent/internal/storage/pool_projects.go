package storage

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"time"

	bolt "go.etcd.io/bbolt"
)

// ProjectIDs owns an installation-reserved project range on one filesystem.
// The private ledger is never mounted in DSH. A database file lock serializes
// allocation AND kernel changes across objects/processes. Reservations commit
// before kernel work; a crash/error retains the ID, never reissues it elsewhere.
type ProjectIDs struct {
	File        string
	First, Last uint32
}

var projectBucket = []byte("pool-project-paths-v1")
var projectConfig = []byte("pool-project-range-v1")

func (p *ProjectIDs) locked(fn func(*bolt.DB) error) error {
	if p == nil || !filepath.IsAbs(p.File) || p.First == 0 || p.Last < p.First {
		return fmt.Errorf("invalid reserved project range")
	}
	if err := os.MkdirAll(filepath.Dir(p.File), 0700); err != nil {
		return err
	}
	if err := CheckNoSymlink(filepath.Dir(p.File), p.File); err != nil {
		return err
	}
	db, err := bolt.Open(p.File, 0600, &bolt.Options{Timeout: 10 * time.Second})
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	err = db.Update(func(tx *bolt.Tx) error {
		if _, e := tx.CreateBucketIfNotExists(projectBucket); e != nil {
			return e
		}
		b, e := tx.CreateBucketIfNotExists(projectConfig)
		if e != nil {
			return e
		}
		var config [8]byte
		binary.BigEndian.PutUint32(config[:4], p.First)
		binary.BigEndian.PutUint32(config[4:], p.Last)
		if old := b.Get([]byte("range")); old != nil && string(old) != string(config[:]) {
			return fmt.Errorf("reserved project range changed")
		}
		return b.Put([]byte("range"), config[:])
	})
	if err != nil {
		return err
	}
	return fn(db)
}

func (p *ProjectIDs) Use(path string, apply func(uint32) error) error {
	if !filepath.IsAbs(path) {
		return fmt.Errorf("absolute quota path required")
	}
	return p.locked(func(db *bolt.DB) error {
		var id uint32
		err := db.Update(func(tx *bolt.Tx) error {
			b := tx.Bucket(projectBucket)
			if raw := b.Get([]byte(path)); raw != nil {
				if len(raw) != 4 {
					return fmt.Errorf("corrupt project ledger")
				}
				id = binary.BigEndian.Uint32(raw)
				if id < p.First || id > p.Last {
					return fmt.Errorf("project outside reserved range")
				}
				return nil
			}
			used := map[uint32]bool{}
			if e := b.ForEach(func(_, v []byte) error {
				if len(v) != 4 {
					return fmt.Errorf("corrupt project ledger")
				}
				used[binary.BigEndian.Uint32(v)] = true
				return nil
			}); e != nil {
				return e
			}
			for candidate := uint64(p.First); candidate <= uint64(p.Last); candidate++ {
				if !used[uint32(candidate)] {
					id = uint32(candidate)
					break
				}
			}
			if id == 0 {
				return fmt.Errorf("reserved project range exhausted")
			}
			var raw [4]byte
			binary.BigEndian.PutUint32(raw[:], id)
			return b.Put([]byte(path), raw[:])
		})
		if err != nil {
			return err
		}
		return apply(id)
	})
}

// Release cannot recycle IDs of live files. Kernel cleanup is performed while
// holding the database lock; failures/crashes keep the reservation for retry.
func (p *ProjectIDs) Release(path string, cleanup func(uint32) error) error {
	if !filepath.IsAbs(path) {
		return fmt.Errorf("absolute quota path required")
	}
	return p.locked(func(db *bolt.DB) error {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			if err != nil {
				return err
			}
			return fmt.Errorf("quota path still exists")
		}
		var id uint32
		if err := db.View(func(tx *bolt.Tx) error {
			raw := tx.Bucket(projectBucket).Get([]byte(path))
			if raw == nil {
				return nil
			}
			if len(raw) != 4 {
				return fmt.Errorf("corrupt project ledger")
			}
			id = binary.BigEndian.Uint32(raw)
			return nil
		}); err != nil {
			return err
		}
		if id == 0 {
			return nil
		}
		if err := cleanup(id); err != nil {
			return err
		}
		return db.Update(func(tx *bolt.Tx) error { return tx.Bucket(projectBucket).Delete([]byte(path)) })
	})
}
