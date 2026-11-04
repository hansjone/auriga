package state

import (
	"context"
	"encoding/json"
	"errors"
	"go.etcd.io/bbolt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func interruptedRecoveryStore(t *testing.T) (*Store, ExecutionRecord) {
	t.Helper()
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	r := executionFixture()
	if err := s.PutNodeMeta(NodeMeta{NodeID: "recovery-node"}); err != nil {
		t.Fatal(err)
	}
	if err := s.PutExecution(r); err != nil {
		t.Fatal(err)
	}
	err = s.WithReleaseExecution(context.Background(), r.Candidate, releaseAllow, func(_ context.Context, e *ReleaseExecution) error {
		f, err := e.BeginAction()
		if err != nil {
			return err
		}
		f(false)
		return nil
	})
	if !errors.Is(err, ErrReleaseUnresolved) {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = OpenOfflineRecovery(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	return s, r
}
func TestOfflineRecoveryOnlyExactCommittedIntent(t *testing.T) {
	for _, mode := range []string{"success", "wrong-expectation", "verifier-failure", "canceled", "higher-water", "different-body"} {
		t.Run(mode, func(t *testing.T) {
			s, r := interruptedRecoveryStore(t)
			snapshot, err := s.InspectInterruptedExecution(r.InstanceID)
			if err != nil {
				t.Fatal(err)
			}
			before, _ := s.GetExecution(r.InstanceID)
			expect := snapshot.Expectation
			if mode == "wrong-expectation" {
				expect = "sha256:" + strings.Repeat("0", 64)
			}
			if mode == "higher-water" || mode == "different-body" {
				c := r.Candidate
				if mode == "higher-water" {
					c.Sequence++
				} else {
					c.BodyDigest = "sha256:" + strings.Repeat("e", 64)
				}
				if err := s.db.Update(func(tx *bbolt.Tx) error { return putReleaseActivationForRecoveryTest(tx, c) }); err != nil {
					t.Fatal(err)
				}
			}
			high, found, err := s.GetReleaseHighWater(r.Candidate.Partition)
			if err != nil || !found {
				t.Fatal(err)
			}
			beforeRows := recoveryRows(t, s)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if mode == "canceled" {
				cancel()
			}
			err = s.RecoverInterruptedExecution(ctx, r.InstanceID, expect, func(context.Context, RecoverySnapshot) error {
				if mode == "verifier-failure" {
					return errors.New("engine unknown")
				}
				return nil
			})
			if (err == nil) != (mode == "success") {
				t.Fatalf("mode %s: %v", mode, err)
			}
			got, found, err := s.GetReleaseHighWater(r.Candidate.Partition)
			if err != nil || !found || got != high {
				t.Fatal("high water changed", err)
			}
			after, _ := s.GetExecution(r.InstanceID)
			if after == nil || after.ContainerID != before.ContainerID || after.Generation != before.Generation {
				t.Fatal("execution changed")
			}
			if mode == "success" {
				delete(beforeRows, string(bucketReleaseIntent)+"/"+r.Candidate.Partition.key())
				if err := s.CheckReleaseExecution(r.Candidate); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := s.CheckReleaseExecution(r.Candidate); !errors.Is(err, ErrReleaseUnresolved) {
					t.Fatal("failed recovery changed intent", err)
				}
			}
			if !reflect.DeepEqual(beforeRows, recoveryRows(t, s)) {
				t.Fatal("recovery changed unrelated persisted rows")
			}
		})
	}
}

func recoveryRows(t *testing.T, s *Store) map[string]string {
	t.Helper()
	out := map[string]string{}
	if err := s.db.View(func(tx *bbolt.Tx) error {
		return tx.ForEach(func(name []byte, b *bbolt.Bucket) error {
			return b.ForEach(func(k, v []byte) error { out[string(name)+"/"+string(k)] = string(v); return nil })
		})
	}); err != nil {
		t.Fatal(err)
	}
	return out
}
func putReleaseActivationForRecoveryTest(tx *bbolt.Tx, c ReleaseCandidate) error {
	raw, err := json.Marshal(releaseActivationRecord{1, c})
	if err != nil {
		return err
	}
	return tx.Bucket(bucketReleaseActivation).Put([]byte(c.Partition.key()), raw)
}
func TestOfflineRecoveryExistingExclusiveOnly(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing")
	if s, err := OpenOfflineRecovery(missing); err == nil {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
		t.Fatal("created missing store")
	}
	dir := t.TempDir()
	live, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := live.Close(); err != nil {
			t.Error(err)
		}
	})
	if s, err := OpenOfflineRecovery(dir); err == nil {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
		t.Fatal("accepted concurrent agent")
	}
	if _, err := live.InspectInterruptedExecution("x"); err == nil {
		t.Fatal("normal store admits recovery")
	}
}
