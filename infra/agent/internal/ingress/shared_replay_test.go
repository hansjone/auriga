package ingress

import (
	"context"
	"go.etcd.io/bbolt"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
)

func TestSharedReplayDurableAndConcurrent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "replay.db")
	db, err := bbolt.Open(path, 0600, nil)
	if err != nil {
		t.Fatal(err)
	}
	s := &SharedIdentityReplayStore{DB: db}
	var successes atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if s.consume(context.Background(), sharedTestID, 160, 100) == nil {
				successes.Add(1)
			}
		}()
	}
	wg.Wait()
	if successes.Load() != 1 {
		t.Fatal(successes.Load())
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = bbolt.Open(path, 0600, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	s.DB = db
	if err = s.consume(context.Background(), sharedTestID, 160, 101); err == nil {
		t.Fatal("restart replay accepted")
	}
	if err = s.consume(context.Background(), sharedTestID, 220, 160); err != nil {
		t.Fatal("expired cache not cleared", err)
	}
}
