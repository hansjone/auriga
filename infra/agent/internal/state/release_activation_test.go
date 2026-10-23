package state

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	nodev1 "github.com/hansjone/auriga/infra/gen/auriga/node/v1"
	"go.etcd.io/bbolt"
	bbolterrors "go.etcd.io/bbolt/errors"
)

func releaseTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	return s
}

func releaseTestCandidate(sequence uint64) ReleaseCandidate {
	// Complete canonical body fixture, excluding signature. Verification belongs
	// to the subsequent verifier, not these storage/callback boundary tests.
	body := fmt.Sprintf(`{"arch":"amd64","archiveDigest":"sha256:%s","artifactId":"dsh-test","dshVersion":"1.0.0","imageDigest":"","issuedAt":"2026-10-23T00:00:00Z","name":"dsh","os":"linux","purpose":"dsh_runtime","releaseSequence":%d,"schemaVersion":1}`, strings.Repeat("a", 64), sequence)
	return ReleaseCandidate{Partition: ReleasePartition{Purpose: "dsh_runtime", Name: "dsh", OS: "linux", Arch: "amd64"}, Sequence: sequence, BodyDigest: fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(body)))}
}

func releaseAllow(context.Context) error { return nil }

func receiveRelease[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case value := <-ch:
		return value
	case <-time.After(5 * time.Second):
		t.Fatal("release synchronization timed out")
		var zero T
		return zero
	}
}

func assertReleaseError(t *testing.T, err, want error) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("error = %v, want %v", err, want)
	}
}

func assertReleaseWater(t *testing.T, s *Store, want ReleaseCandidate) {
	t.Helper()
	got, found, err := s.GetReleaseHighWater(want.Partition)
	if err != nil || !found || got != want {
		t.Fatalf("high water = %+v, %v, %v; want %+v", got, found, err, want)
	}
}

// Done observation proves a contender has reached the semaphore select. It does
// not add a production hook, scheduler delay or memory-only model of the DB.
type observedReleaseContext struct {
	context.Context
	once    sync.Once
	waiting chan struct{}
}

func (c *observedReleaseContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.waiting) })
	return c.Context.Done()
}

func observeReleaseContext(ctx context.Context) *observedReleaseContext {
	return &observedReleaseContext{Context: ctx, waiting: make(chan struct{})}
}

// A manually fired deadline avoids scheduler timing in the queued-timeout case.
type releaseExpiryContext struct {
	context.Context
	reason error
}

func (c releaseExpiryContext) Err() error {
	if c.Context.Err() != nil {
		return c.reason
	}
	return nil
}

func TestReleaseActivationReopenAndDesiredDeletion(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	c := releaseTestCandidate(7)
	if got, found, err := s.GetReleaseHighWater(c.Partition); err != nil || found || got != (ReleaseCandidate{}) {
		t.Fatalf("empty = %+v %v %v", got, found, err)
	}
	if err := s.WithReleaseActivation(context.Background(), c, releaseAllow, releaseAllow); err != nil {
		t.Fatal(err)
	}
	if err := s.PutDesired(&nodev1.InstanceSpec{InstanceId: "instance-a", Generation: 99}); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteDesired("instance-a"); err != nil {
		t.Fatal(err)
	}
	assertReleaseWater(t, s, c)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	_, _, err = s.GetReleaseHighWater(c.Partition)
	assertReleaseError(t, err, bbolterrors.ErrDatabaseNotOpen)
	s, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	assertReleaseWater(t, s, c)
	for _, attempt := range []struct {
		candidate ReleaseCandidate
		want      error
	}{
		{releaseTestCandidate(6), ErrReleaseRollback},
		{ReleaseCandidate{Partition: c.Partition, Sequence: 7, BodyDigest: releaseTestCandidate(8).BodyDigest}, ErrReleaseConflict},
	} {
		called := false
		err := s.WithReleaseActivation(context.Background(), attempt.candidate, releaseAllow, func(context.Context) error { called = true; return nil })
		assertReleaseError(t, err, attempt.want)
		if called {
			t.Fatal("rejected activation called")
		}
	}
	// No signing key is persisted: identical body may be authorized by a new key.
	for range 2 {
		if err := s.WithReleaseActivation(context.Background(), c, releaseAllow, releaseAllow); err != nil {
			t.Fatal(err)
		}
	}
	assertReleaseWater(t, s, c)
}

func TestReleaseActivationContinuousQualification(t *testing.T) {
	s := releaseTestStore(t)
	a, b := releaseTestCandidate(7), releaseTestCandidate(8)
	entered, finish, doneA := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	defer close(finish)
	ctxA, cancelA := context.WithCancel(context.Background())
	defer cancelA()
	go func() {
		doneA <- s.WithReleaseActivation(ctxA, a, releaseAllow, func(context.Context) error { close(entered); <-finish; return nil })
	}()
	receiveRelease(t, entered)
	assertReleaseWater(t, s, a)
	ctxB := observeReleaseContext(context.Background())
	authorizedB, doneB := make(chan struct{}), make(chan error, 1)
	go func() {
		doneB <- s.WithReleaseActivation(ctxB, b, func(context.Context) error { close(authorizedB); return nil }, releaseAllow)
	}()
	receiveRelease(t, ctxB.waiting)
	cancelA() // Cancellation must not release A while its callback is running.
	select {
	case <-authorizedB:
		t.Fatal("higher sequence overtook active callback")
	default:
	}
	assertReleaseWater(t, s, a)
	// Every other OS/architecture partition remains available.
	for _, p := range []ReleasePartition{
		{Purpose: "dsh_runtime", Name: "dsh", OS: "linux", Arch: "arm64"},
		{Purpose: "dsh_runtime", Name: "dsh", OS: "windows", Arch: "amd64"},
		{Purpose: "dsh_runtime", Name: "dsh", OS: "windows", Arch: "arm64"},
	} {
		other := b
		other.Partition = p
		if err := s.WithReleaseActivation(context.Background(), other, releaseAllow, releaseAllow); err != nil {
			t.Fatal(err)
		}
		assertReleaseWater(t, s, other)
	}
	finish <- struct{}{}
	assertReleaseError(t, receiveRelease(t, doneA), nil)
	receiveRelease(t, authorizedB)
	assertReleaseError(t, receiveRelease(t, doneB), nil)
	assertReleaseWater(t, s, b)
	assertReleaseError(t, s.WithReleaseActivation(context.Background(), a, releaseAllow, releaseAllow), ErrReleaseRollback)
	b.BodyDigest = a.BodyDigest
	assertReleaseError(t, s.WithReleaseActivation(context.Background(), b, releaseAllow, releaseAllow), ErrReleaseConflict)
}

func TestReleaseActivationConcurrentCommits(t *testing.T) {
	s := releaseTestStore(t)
	start, done := make(chan struct{}), make(chan error, 32)
	for sequence := uint64(1); sequence <= 32; sequence++ {
		go func() {
			<-start
			done <- s.WithReleaseActivation(context.Background(), releaseTestCandidate(sequence), releaseAllow, releaseAllow)
		}()
	}
	close(start)
	for range 32 {
		if err := receiveRelease(t, done); err != nil && !errors.Is(err, ErrReleaseRollback) {
			t.Fatal(err)
		}
	}
	assertReleaseWater(t, s, releaseTestCandidate(32))
}

func TestReleaseActivationQueuedCancellation(t *testing.T) {
	for _, reason := range []error{context.Canceled, context.DeadlineExceeded} {
		t.Run(reason.Error(), func(t *testing.T) {
			s := releaseTestStore(t)
			c := releaseTestCandidate(1)
			// Hold the real Store semaphore to make queue admission deterministic.
			<-s.activationLocks[0]
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			observed := observeReleaseContext(releaseExpiryContext{Context: ctx, reason: reason})
			done := make(chan error, 1)
			go func() {
				done <- s.WithReleaseActivation(observed, c, func(context.Context) error { t.Error("queued authorize called"); return nil }, releaseAllow)
			}()
			receiveRelease(t, observed.waiting)
			cancel()
			assertReleaseError(t, receiveRelease(t, done), reason)
			s.activationLocks[0] <- struct{}{}
			if err := s.WithReleaseActivation(context.Background(), c, releaseAllow, releaseAllow); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestReleaseActivationFailuresAndPanics(t *testing.T) {
	for _, phase := range []string{"authorize error", "authorize panic", "activate error", "activate panic", "authorize cancellation", "database closed", "database read only"} {
		t.Run(phase, func(t *testing.T) {
			s := releaseTestStore(t)
			old, next := releaseTestCandidate(7), releaseTestCandidate(8)
			if err := s.WithReleaseActivation(context.Background(), old, releaseAllow, releaseAllow); err != nil {
				t.Fatal(err)
			}
			failure := errors.New("callback failure")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			called := false
			authorize := func(context.Context) error {
				switch phase {
				case "authorize error":
					return failure
				case "authorize panic":
					panic(failure)
				case "authorize cancellation":
					cancel()
				case "database closed":
					return s.db.Close()
				case "database read only":
					path := s.db.Path()
					if err := s.db.Close(); err != nil {
						return err
					}
					var err error
					s.db, err = bbolt.Open(path, 0o600, &bbolt.Options{ReadOnly: true})
					return err
				}
				return nil
			}
			activate := func(context.Context) error {
				called = true
				if phase == "activate panic" {
					panic(failure)
				}
				return failure
			}
			var got error
			var recovered any
			func() {
				defer func() { recovered = recover() }()
				got = s.WithReleaseActivation(ctx, next, authorize, activate)
			}()
			if strings.HasSuffix(phase, "panic") {
				if recovered != failure {
					t.Fatalf("panic = %v", recovered)
				}
			} else {
				want := failure
				if phase == "authorize cancellation" {
					want = context.Canceled
				}
				if phase == "database closed" {
					want = bbolterrors.ErrDatabaseNotOpen
				}
				if phase == "database read only" {
					want = bbolterrors.ErrDatabaseReadOnly
				}
				assertReleaseError(t, got, want)
			}
			if called != strings.HasPrefix(phase, "activate") {
				t.Fatalf("activate called = %v", called)
			}
			if phase == "database closed" {
				return
			}
			if called {
				assertReleaseWater(t, s, next)
			} else {
				assertReleaseWater(t, s, old)
			}
			if phase == "database read only" {
				return
			}
			if err := s.WithReleaseActivation(context.Background(), next, releaseAllow, releaseAllow); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestReleaseActivationCloseLifecycle(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	c := releaseTestCandidate(7)
	entered, finish, activeDone := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	defer close(finish)
	go func() {
		activeDone <- s.WithReleaseActivation(context.Background(), c, releaseAllow, func(context.Context) error { close(entered); <-finish; return nil })
	}()
	receiveRelease(t, entered)
	queued := observeReleaseContext(context.Background())
	queueDone := make(chan error, 1)
	go func() {
		queueDone <- s.WithReleaseActivation(queued, releaseTestCandidate(8), func(context.Context) error { t.Error("closing queued authorize called"); return nil }, releaseAllow)
	}()
	receiveRelease(t, queued.waiting)
	closeDone, secondClose := make(chan error, 1), make(chan error, 1)
	go func() { closeDone <- s.Close() }()
	receiveRelease(t, s.activationStop)
	go func() { secondClose <- s.Close() }()
	assertReleaseError(t, receiveRelease(t, queueDone), bbolterrors.ErrDatabaseNotOpen)
	assertReleaseError(t, s.WithReleaseActivation(context.Background(), releaseTestCandidate(9), releaseAllow, releaseAllow), bbolterrors.ErrDatabaseNotOpen)
	select {
	case err := <-closeDone:
		t.Fatalf("Close returned before callback: %v", err)
	default:
	}
	assertReleaseWater(t, s, c) // DB remains open while actual activation is active.
	finish <- struct{}{}
	assertReleaseError(t, receiveRelease(t, activeDone), nil)
	assertReleaseError(t, receiveRelease(t, closeDone), nil)
	assertReleaseError(t, receiveRelease(t, secondClose), nil)
	reopened, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reopened.Close() }()
	assertReleaseWater(t, reopened, c)
	assertReleaseError(t, reopened.WithReleaseActivation(context.Background(), releaseTestCandidate(8), releaseAllow, releaseAllow), nil)
}

func TestReleaseActivationCorruptRecord(t *testing.T) {
	valid := releaseTestCandidate(7)
	// The exact JSON persisted below is validated through real bbolt reads and
	// write transactions, not a separate test-only parser.
	base := fmt.Sprintf(`{"version":1,"candidate":{"partition":{"purpose":"dsh_runtime","name":"dsh","os":"linux","arch":"amd64"},"sequence":7,"bodyDigest":"%s"}}`, valid.BodyDigest)
	for name, raw := range map[string]string{
		"bad JSON": "{", "trailing": base + "{}", "unknown": strings.Replace(base, `"version":1`, `"version":1,"extra":0`, 1),
		"version": strings.Replace(base, `"version":1`, `"version":2`, 1),
		"missing": strings.Replace(base, `"version":1,`, "", 1), "null": "null",
		"duplicate": strings.Replace(base, `"version":1`, `"version":1,"version":1`, 1),
		"purpose":   strings.Replace(base, "dsh_runtime", "plugin", 1), "OS": strings.Replace(base, "linux", "any", 1),
		"arch": strings.Replace(base, "amd64", "any", 1), "name": strings.Replace(base, `"name":"dsh"`, `"name":"other"`, 1),
		"zero":      strings.Replace(base, `"sequence":7`, `"sequence":0`, 1),
		"unsafe":    strings.Replace(base, `"sequence":7`, `"sequence":9007199254740992`, 1),
		"fraction":  strings.Replace(base, `"sequence":7`, `"sequence":7.5`, 1),
		"digest":    strings.Replace(base, valid.BodyDigest, "sha256:bad", 1),
		"uppercase": strings.Replace(base, valid.BodyDigest, strings.ToUpper(valid.BodyDigest), 1),
	} {
		t.Run(name, func(t *testing.T) {
			s := releaseTestStore(t)
			if err := s.db.Update(func(tx *bbolt.Tx) error {
				b, err := tx.CreateBucketIfNotExists(bucketReleaseActivation)
				if err != nil {
					return err
				}
				return b.Put([]byte(valid.Partition.key()), []byte(raw))
			}); err != nil {
				t.Fatal(err)
			}
			_, _, err := s.GetReleaseHighWater(valid.Partition)
			assertReleaseError(t, err, ErrReleaseStateCorrupt)
			called := false
			err = s.WithReleaseActivation(context.Background(), releaseTestCandidate(8), releaseAllow, func(context.Context) error { called = true; return nil })
			assertReleaseError(t, err, ErrReleaseStateCorrupt)
			if called {
				t.Fatal("corrupt state activated")
			}
		})
	}
	for _, nested := range []bool{false, true} {
		t.Run(fmt.Sprintf("key-binding-nested-%v", nested), func(t *testing.T) {
			s := releaseTestStore(t)
			if err := s.db.Update(func(tx *bbolt.Tx) error {
				b, err := tx.CreateBucketIfNotExists(bucketReleaseActivation)
				if err != nil {
					return err
				}
				if nested {
					_, err = b.CreateBucket([]byte(valid.Partition.key()))
					return err
				}
				return b.Put([]byte("wrong-key"), []byte(base))
			}); err != nil {
				t.Fatal(err)
			}
			_, _, err := s.GetReleaseHighWater(valid.Partition)
			assertReleaseError(t, err, ErrReleaseStateCorrupt)
			assertReleaseError(t, s.WithReleaseActivation(context.Background(), releaseTestCandidate(8), releaseAllow, releaseAllow), ErrReleaseStateCorrupt)
		})
	}
}

func TestReleaseActivationInvalidInput(t *testing.T) {
	s := releaseTestStore(t)
	valid := releaseTestCandidate(1)
	for _, invalid := range []ReleaseCandidate{
		{}, {Partition: valid.Partition, Sequence: 0, BodyDigest: valid.BodyDigest},
		{Partition: valid.Partition, Sequence: maxReleaseSequence + 1, BodyDigest: valid.BodyDigest},
		{Partition: valid.Partition, Sequence: 1, BodyDigest: "sha256:" + strings.Repeat("A", 64)},
		{Partition: valid.Partition, Sequence: 1, BodyDigest: "sha256:" + strings.Repeat("g", 64)},
	} {
		called := false
		callback := func(context.Context) error { called = true; return nil }
		if err := s.WithReleaseActivation(context.Background(), invalid, callback, callback); err == nil || called {
			t.Fatalf("invalid input: %v, called %v", err, called)
		}
	}
	for _, callbacks := range [][2]func(context.Context) error{{nil, releaseAllow}, {releaseAllow, nil}} {
		if err := s.WithReleaseActivation(context.Background(), valid, callbacks[0], callbacks[1]); err == nil {
			t.Fatal("nil callback accepted")
		}
	}
	if _, _, err := s.GetReleaseHighWater(ReleasePartition{}); err == nil {
		t.Fatal("invalid query partition accepted")
	}
	ctx, cancel := context.WithDeadline(context.Background(), time.Unix(0, 0))
	defer cancel()
	called := false
	callback := func(context.Context) error { called = true; return nil }
	assertReleaseError(t, s.WithReleaseActivation(ctx, valid, callback, callback), context.DeadlineExceeded)
	if called {
		t.Fatal("expired context authorized or activated")
	}
	valid.Sequence = maxReleaseSequence
	assertReleaseError(t, s.WithReleaseActivation(context.Background(), valid, releaseAllow, releaseAllow), nil)
	assertReleaseWater(t, s, valid)
}
