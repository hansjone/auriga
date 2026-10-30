package relayhttp

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hansjone/auriga/infra/ai-gateway/internal/relay"
	api "github.com/hansjone/auriga/infra/gen/aiapi"
	"github.com/jackc/pgx/v5"
)

type keyReply struct {
	status          int
	requestID, body string
	err             error
}

// Use Go's real HTTP client: Node/fetch header restrictions cannot narrow the
// formal UTF-8 contract. Preserve all header bytes and duplicate field values.
func (f *fixture) keyRequest(ctx context.Context, s *httptest.Server, keys []string, body string) keyReply {
	r, err := http.NewRequestWithContext(ctx, "POST", s.URL+chatPath, strings.NewReader(body))
	if err != nil {
		return keyReply{err: err}
	}
	r.Header.Set("Authorization", "Bearer "+f.raw)
	r.Header.Set("Content-Type", "application/json")
	r.Header["Idempotency-Key"] = keys
	res, err := s.Client().Do(r)
	if err != nil {
		return keyReply{err: err}
	}
	defer func() { _ = res.Body.Close() }()
	b, err := io.ReadAll(res.Body)
	return keyReply{status: res.StatusCode, requestID: res.Header.Get("X-Request-Id"), body: string(b), err: err}
}

func keyError(t *testing.T, r keyReply, status int, code string) api.AiError {
	t.Helper()
	var e api.AiError
	if r.err != nil || r.status != status {
		t.Fatalf("HTTP %d: %s (%v)", r.status, r.body, r.err)
	}
	if err := json.Unmarshal([]byte(r.body), &e); err != nil || e.Error.Code != code || e.RequestId == "" {
		t.Fatalf("%s: %v", r.body, err)
	}
	return e
}

func (f *fixture) keyFacts(t *testing.T, requests, active, held, outbox int64) {
	t.Helper()
	err := f.db.Transaction(t.Context(), f.control.tenant, func(tx pgx.Tx) error {
		var n, a, h, b int64
		if err := tx.QueryRow(t.Context(), `SELECT count(*) FROM ai.budget_request WHERE tenant_id=$1`, f.control.tenant).Scan(&n); err != nil {
			return err
		}
		if err := tx.QueryRow(t.Context(), `SELECT coalesce(sum(active),0),coalesce(sum(held_tokens),0) FROM ai.budget_subject WHERE tenant_id=$1`, f.control.tenant).Scan(&a, &h); err != nil {
			return err
		}
		if err := tx.QueryRow(t.Context(), `SELECT count(*) FROM ai.budget_outbox WHERE tenant_id=$1`, f.control.tenant).Scan(&b); err != nil {
			return err
		}
		if n != requests || a != active || h != held || b != outbox {
			t.Errorf("request/slot/hold/outbox=%d/%d/%d/%d want %d/%d/%d/%d", n, a, h, b, requests, active, held, outbox)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestHTTPUTF8IdempotencyKeyBoundaries(t *testing.T) {
	for _, c := range []struct {
		name  string
		keys  []string
		valid bool
	}{
		{"ascii128", []string{strings.Repeat("a", 128)}, true},
		{"han85", []string{strings.Repeat("中", 85)}, true},
		{"han86", []string{strings.Repeat("中", 86)}, true},
		{"han128", []string{strings.Repeat("中", 128)}, true},
		{"fourbyte128", []string{strings.Repeat("😀", 128)}, true},
		{"ascii129", []string{strings.Repeat("a", 129)}, false},
		{"han129", []string{strings.Repeat("中", 129)}, false},
		{"fourbyte129", []string{strings.Repeat("😀", 129)}, false},
		{"badUTF8", []string{string([]byte{0xff})}, false},
		{"duplicate", []string{strings.Repeat("中", 128), strings.Repeat("中", 128)}, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := fixtureNew(t)
			var calls atomic.Int32
			s := f.server(t, finite(completion(textChoice), &calls))
			r := f.keyRequest(t.Context(), s, c.keys, plainRequest)
			if !c.valid {
				keyError(t, r, 400, "common.validation_failed")
				if calls.Load() != 0 {
					t.Fatal("rejected key submitted upstream")
				}
				f.keyFacts(t, 0, 0, 0, 0)
				return
			}
			if r.err != nil || r.status != 200 || r.requestID == "" || calls.Load() != 1 {
				t.Fatalf("%d-byte key: HTTP %d %s (%v) calls=%d", len(c.keys[0]), r.status, r.body, r.err, calls.Load())
			}
			f.assertState(t, "completed", 0, 0)
			// Verify the durable hash is of the original header, with no HTTP-side
			// digest/prefix/truncation that could alias callers or direct Core users.
			hash := sha256.Sum256([]byte(c.keys[0]))
			err := f.db.Transaction(t.Context(), f.control.tenant, func(tx pgx.Tx) error {
				var stored []byte
				if err := tx.QueryRow(t.Context(), `SELECT key_hash FROM ai.budget_request WHERE tenant_id=$1 AND request_id=$2`, f.control.tenant, r.requestID).Scan(&stored); err != nil {
					return err
				}
				if string(stored) != string(hash[:]) {
					t.Fatal("original key hashing changed")
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestHTTPEmptyAndAbsentKeysRemainIndependentRequests(t *testing.T) {
	f := fixtureNew(t)
	var calls atomic.Int32
	s := f.server(t, finite(completion(textChoice), &calls))
	ids := map[string]bool{}
	for _, keys := range [][]string{nil, {""}, nil, {""}} {
		r := f.keyRequest(t.Context(), s, keys, plainRequest)
		if r.err != nil || r.status != 200 || r.requestID == "" || ids[r.requestID] {
			t.Fatalf("empty/absent key replayed or rejected: %+v", r)
		}
		ids[r.requestID] = true
	}
	if calls.Load() != 4 {
		t.Fatal("empty/absent keys did not execute independently")
	}
	f.keyFacts(t, 4, 0, 0, 4)
	err := f.db.Transaction(t.Context(), f.control.tenant, func(tx pgx.Tx) error {
		var hashed int
		if err := tx.QueryRow(t.Context(), `SELECT count(*) FROM ai.budget_request WHERE tenant_id=$1 AND key_hash IS NOT NULL`, f.control.tenant).Scan(&hashed); err != nil {
			return err
		}
		if hashed != 0 {
			t.Error("empty/absent keys acquired idempotency hashes")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestHTTPUTF8IdempotencyConcurrencyAndReplay(t *testing.T) {
	for _, c := range []struct{ name, key string }{
		{"han86", strings.Repeat("中", 86)},
		{"han128", strings.Repeat("中", 128)},
		{"fourbyte128", strings.Repeat("😀", 128)},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := fixtureNew(t)
			var calls atomic.Int32
			entered, release := make(chan struct{}, 1), make(chan struct{})
			var once sync.Once
			defer once.Do(func() { close(release) })
			s := f.server(t, func(ctx context.Context, s relay.Submission, key []byte) (relay.Exchange, error) {
				calls.Add(1)
				select {
				case entered <- struct{}{}:
				default:
				}
				select {
				case <-release:
				case <-ctx.Done():
					return relay.Exchange{}, ctx.Err()
				}
				stop := make(chan struct{})
				close(stop)
				return relay.Exchange{Body: io.NopCloser(strings.NewReader(completion(textChoice))), Stopped: stop}, nil
			})
			ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
			defer cancel()
			first := make(chan keyReply, 1)
			go func() { first <- f.keyRequest(ctx, s, []string{c.key}, plainRequest) }()
			select {
			case <-entered:
			case <-ctx.Done():
				t.Fatal("initial submission not reached")
			}
			inflight := keyError(t, f.keyRequest(ctx, s, []string{c.key}, plainRequest), 409, "ai.request_in_progress")
			if inflight.GetOriginalRequestId() == "" {
				t.Fatal("lost original in-flight request ID")
			}
			keyError(t, f.keyRequest(ctx, s, []string{c.key}, strings.Replace(plainRequest, "private prompt", "different prompt", 1)), 409, "common.conflict")
			f.keyFacts(t, 1, 1, 48, 0)
			if calls.Load() != 1 {
				t.Fatal("multiple concurrent submissions")
			}
			once.Do(func() { close(release) })
			var initial keyReply
			select {
			case initial = <-first:
			case <-ctx.Done():
				t.Fatal("initial response blocked")
			}
			if initial.err != nil || initial.status != 200 || initial.requestID != inflight.GetOriginalRequestId() {
				t.Fatal(initial, inflight)
			}
			replay := keyError(t, f.keyRequest(ctx, s, []string{c.key}, plainRequest), 409, "ai.replay_unavailable")
			if replay.GetOriginalRequestId() != initial.requestID || replay.GetOutcome() != "completed" {
				t.Fatal("lost terminal identity/outcome", replay)
			}
			if calls.Load() != 1 {
				t.Fatal("terminal retry re-executed")
			}
			f.assertState(t, "completed", 0, 0)
		})
	}
}

func TestHTTPDistinctLongUTF8KeysDoNotAlias(t *testing.T) {
	f := fixtureNew(t)
	var calls atomic.Int32
	s := f.server(t, finite(completion(textChoice), &calls))
	ids := map[string]bool{}
	for _, key := range []string{strings.Repeat("😀", 127) + "a", strings.Repeat("😀", 127) + "b"} {
		r := f.keyRequest(t.Context(), s, []string{key}, plainRequest)
		if r.err != nil || r.status != 200 || r.requestID == "" || ids[r.requestID] {
			t.Fatalf("distinct long key aliased: %+v", r)
		}
		ids[r.requestID] = true
		replay := keyError(t, f.keyRequest(t.Context(), s, []string{key}, plainRequest), 409, "ai.replay_unavailable")
		if replay.GetOriginalRequestId() != r.requestID {
			t.Fatal("distinct key replay changed original")
		}
	}
	if calls.Load() != 2 {
		t.Fatal("distinct keys did not execute independently")
	}
	f.keyFacts(t, 2, 0, 0, 2)
}

func TestHTTPLongUTF8KeyOwnerTenantIsolation(t *testing.T) {
	f := fixtureNew(t)
	owner := f.secondOwner(t)
	tenant := fixtureNew(t)
	var calls atomic.Int32
	s := f.server(t, finite(completion(textChoice), &calls))
	other := tenant.server(t, finite(completion(textChoice), &calls))
	key := strings.Repeat("😀", 128)
	ids := map[string]bool{}
	for _, pair := range []struct {
		f *fixture
		s *httptest.Server
	}{{f, s}, {owner, s}, {tenant, other}} {
		r := pair.f.keyRequest(t.Context(), pair.s, []string{key}, plainRequest)
		if r.err != nil || r.status != 200 || r.requestID == "" || ids[r.requestID] {
			t.Fatalf("cross-owner/tenant key collision: %+v", r)
		}
		ids[r.requestID] = true
		replay := keyError(t, pair.f.keyRequest(t.Context(), pair.s, []string{key}, plainRequest), 409, "ai.replay_unavailable")
		if replay.GetOriginalRequestId() != r.requestID || replay.GetOutcome() != "completed" {
			t.Fatal("cross-owner/tenant replay leak", replay)
		}
	}
	if calls.Load() != 3 {
		t.Fatal("cross-owner/tenant execution count", calls.Load())
	}
	f.keyFacts(t, 2, 0, 0, 2)
	tenant.keyFacts(t, 1, 0, 0, 1)
}
