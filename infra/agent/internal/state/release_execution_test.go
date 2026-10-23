package state

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	nodev1 "github.com/hansjone/auriga/infra/gen/auriga/node/v1"
	"go.etcd.io/bbolt"
	bbolterrors "go.etcd.io/bbolt/errors"
)

func executionFixture() ExecutionRecord {
	c := releaseTestCandidate(7)
	sum := "sha256:" + strings.Repeat("b", 64)
	config := "sha256:" + strings.Repeat("d", 64)
	return ExecutionRecord{Version: 2, InstanceID: "execution-test", Generation: 1, DSH: &nodev1.DshRuntime{ArtifactId: "original", Digest: "sha256:" + strings.Repeat("a", 64), ReleaseEnvelope: &nodev1.RuntimeReleaseEnvelope{Purpose: c.Partition.Purpose, Name: c.Partition.Name, Os: c.Partition.OS, Arch: c.Partition.Arch, ReleaseSequence: c.Sequence, ImageDigest: sum}}, Candidate: c, ContainerID: strings.Repeat("c", 64), ConfigID: config, EngineImageID: config, ProofDigest: sum, Phase: "created"}
}

func TestExecutionIsIndependentDeepCopiedAndDurable(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	r := executionFixture()
	if err := s.PutExecution(r); err != nil {
		t.Fatal(err)
	}
	r.DSH.ArtifactId = "caller-mutated"
	got, err := s.GetExecution(r.InstanceID)
	if err != nil || got.DSH.ArtifactId != "original" {
		t.Fatal(got, err)
	}
	got.DSH.ArtifactId = "read-mutated"
	if err := s.PutActual(ActualRecord{Status: &nodev1.InstanceStatus{InstanceId: r.InstanceID, ObservedGeneration: 9}}); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteDesired(r.InstanceID); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	got, err = s.GetExecution(r.InstanceID)
	if err != nil || got == nil || got.Generation != 1 || got.DSH.ArtifactId != "original" {
		t.Fatal(got, err)
	}
	if err := s.DeleteExecution(r.InstanceID, strings.Repeat("d", 64)); !errors.Is(err, ErrExecutionCorrupt) {
		t.Fatal("stale delete accepted", err)
	}
	if err := s.DeleteExecution(r.InstanceID, r.ContainerID); err != nil {
		t.Fatal(err)
	}
	got, err = s.GetExecution(r.InstanceID)
	if err != nil || got != nil {
		t.Fatal(got, err)
	}
}

func TestExecutionCorruptionFailsEveryReadWrite(t *testing.T) {
	for _, mode := range []string{"truncated", "unknown", "duplicate", "null", "wrong-key", "bad-record", "nested", "old-version", "missing-engine-id", "foreign-engine-id", "invalid-engine-id"} {
		t.Run(mode, func(t *testing.T) {
			s := releaseTestStore(t)
			r := executionFixture()
			raw, err := json.Marshal(r)
			if err != nil {
				t.Fatal(err)
			}
			if err := s.db.Update(func(tx *bbolt.Tx) error {
				b, err := tx.CreateBucketIfNotExists(bucketReleaseExecution)
				if err != nil {
					return err
				}
				key := []byte(r.InstanceID)
				switch mode {
				case "truncated":
					raw = []byte("{")
				case "unknown":
					raw = append([]byte(`{"unknown":1,`), raw[1:]...)
				case "duplicate":
					raw = append([]byte(`{"version":1,`), raw[1:]...)
				case "null":
					raw = []byte("null")
				case "wrong-key":
					key = []byte("another-instance")
				case "bad-record":
					r.Generation = 0
					raw, _ = json.Marshal(r)
				case "old-version":
					r.Version = 1
					raw, _ = json.Marshal(r)
				case "missing-engine-id":
					var fields map[string]any
					_ = json.Unmarshal(raw, &fields)
					delete(fields, "engineImageId")
					raw, _ = json.Marshal(fields)
				case "foreign-engine-id":
					r.EngineImageID = "sha256:" + strings.Repeat("e", 64)
					raw, _ = json.Marshal(r)
				case "invalid-engine-id":
					r.EngineImageID = "sha256:invalid"
					raw, _ = json.Marshal(r)
				case "nested":
					_, err := b.CreateBucket(key)
					return err
				}
				return b.Put(key, raw)
			}); err != nil {
				t.Fatal(err)
			}
			if _, err := s.GetExecution(r.InstanceID); !errors.Is(err, ErrExecutionCorrupt) {
				t.Fatal(err)
			}
			if _, err := s.ListExecutions(); !errors.Is(err, ErrExecutionCorrupt) {
				t.Fatal(err)
			}
			if err := s.PutExecution(executionFixture()); !errors.Is(err, ErrExecutionCorrupt) {
				t.Fatal(err)
			}
			if err := s.DeleteExecution(r.InstanceID, r.ContainerID); !errors.Is(err, ErrExecutionCorrupt) {
				t.Fatal(err)
			}
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			if reopened, err := Open(s.agentDir); !errors.Is(err, ErrExecutionCorrupt) {
				if reopened != nil {
					_ = reopened.Close()
				}
				t.Fatal("corrupt execution reopened", err)
			}
		})
	}
}

func TestExecutionManifestEngineIdentityIsDurable(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	r := executionFixture()
	r.EngineImageID = r.ProofDigest
	if err := s.PutExecution(r); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	got, err := s.GetExecution(r.InstanceID)
	if err != nil || got == nil || got.EngineImageID != r.ProofDigest || got.ConfigID != r.ConfigID {
		t.Fatal(got, err)
	}
}

func TestIntentSurvivesUnknownFailurePanicAndReopen(t *testing.T) {
	for _, mode := range []string{"unknown", "panic", "canceled"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			s, err := Open(dir)
			if err != nil {
				t.Fatal(err)
			}
			c := releaseTestCandidate(7)
			ctx, cancel := context.WithCancel(context.Background())
			func() {
				defer func() {
					if mode == "panic" && recover() == nil {
						t.Fatal("panic swallowed")
					}
				}()
				_ = s.WithReleaseExecution(ctx, c, releaseAllow, func(_ context.Context, e *ReleaseExecution) error {
					finish, err := e.BeginAction()
					if err != nil {
						return err
					}
					if mode == "panic" {
						panic("original domain died")
					}
					if mode == "canceled" {
						cancel()
					}
					finish(false)
					return errors.New("unknown Engine result")
				})
			}()
			cancel()
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			s, err = Open(dir)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = s.Close() }()
			for _, next := range []ReleaseCandidate{c, releaseTestCandidate(8)} {
				called := false
				err := s.WithReleaseExecution(context.Background(), next, releaseAllow, func(context.Context, *ReleaseExecution) error { called = true; return nil })
				if !errors.Is(err, ErrReleaseUnresolved) || called {
					t.Fatal("unresolved intent bypassed", err)
				}
			}
			assertReleaseWater(t, s, c)
		})
	}
}

func TestIntentHealthyFailureCanClearButUnknownCannotBeOverwritten(t *testing.T) {
	s := releaseTestStore(t)
	c := releaseTestCandidate(7)
	healthErr := errors.New("unhealthy after definite Engine reply")
	err := s.WithReleaseExecution(context.Background(), c, releaseAllow, func(_ context.Context, e *ReleaseExecution) error {
		finish, err := e.BeginAction()
		if err != nil {
			return err
		}
		finish(true)
		return healthErr
	})
	if !errors.Is(err, healthErr) {
		t.Fatal(err)
	}
	err = s.WithReleaseExecution(context.Background(), c, releaseAllow, func(_ context.Context, e *ReleaseExecution) error {
		lost, err := e.BeginAction()
		if err != nil {
			return err
		}
		lost(false)
		later, err := e.BeginAction()
		if err != nil {
			return err
		}
		later(true)
		return nil
	})
	if !errors.Is(err, ErrReleaseUnresolved) {
		t.Fatal("later reply erased earlier uncertainty", err)
	}
	if err := s.CheckReleaseExecution(c); !errors.Is(err, ErrReleaseUnresolved) {
		t.Fatal(err)
	}
}

func TestExecutionAndIntentClosedDBReject(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if err := s.PutExecution(executionFixture()); !errors.Is(err, bbolterrors.ErrDatabaseNotOpen) {
		t.Fatal(err)
	}
	if _, err := s.GetExecution("execution-test"); !errors.Is(err, bbolterrors.ErrDatabaseNotOpen) {
		t.Fatal(err)
	}
	if err := s.WithReleaseExecution(context.Background(), releaseTestCandidate(7), releaseAllow, func(context.Context, *ReleaseExecution) error { t.Fatal("closed db executed"); return nil }); !errors.Is(err, bbolterrors.ErrDatabaseNotOpen) {
		t.Fatal(err)
	}
}

func TestIntentCorruptionCannotAuthorizeOrReopen(t *testing.T) {
	for _, raw := range []string{`null`, `{}`, `{"version":1,"version":1}`, `{"version":1,"token":"bad","candidate":null}`} {
		t.Run(raw, func(t *testing.T) {
			s := releaseTestStore(t)
			if err := s.db.Update(func(tx *bbolt.Tx) error {
				b, err := tx.CreateBucketIfNotExists(bucketReleaseIntent)
				if err != nil {
					return err
				}
				return b.Put([]byte(releaseTestCandidate(7).Partition.key()), []byte(raw))
			}); err != nil {
				t.Fatal(err)
			}
			if err := s.WithReleaseExecution(context.Background(), releaseTestCandidate(8), releaseAllow, func(context.Context, *ReleaseExecution) error { t.Fatal("corrupt intent executed"); return nil }); !errors.Is(err, ErrExecutionCorrupt) {
				t.Fatal(err)
			}
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			if reopened, err := Open(s.agentDir); !errors.Is(err, ErrExecutionCorrupt) {
				if reopened != nil {
					_ = reopened.Close()
				}
				t.Fatal(err)
			}
		})
	}
}

func TestIntentClearCommitFailureKeepsPartitionBlocked(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	err = s.WithReleaseExecution(context.Background(), releaseTestCandidate(7), releaseAllow, func(_ context.Context, e *ReleaseExecution) error {
		finish, err := e.BeginAction()
		if err != nil {
			return err
		}
		finish(true)
		if err := s.db.Close(); err != nil {
			return err
		}
		s.db, err = bbolt.Open(filepath.Join(dir, "state.db"), 0600, &bbolt.Options{ReadOnly: true})
		return err
	})
	if !errors.Is(err, bbolterrors.ErrDatabaseReadOnly) {
		t.Fatal("clear commit failure lost", err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	if err := s.WithReleaseExecution(context.Background(), releaseTestCandidate(8), releaseAllow, func(context.Context, *ReleaseExecution) error {
		t.Fatal("failed clear allowed higher activation")
		return nil
	}); !errors.Is(err, ErrReleaseUnresolved) {
		t.Fatal(err)
	}
	assertReleaseWater(t, s, releaseTestCandidate(7))
}
