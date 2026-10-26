package relayhttp

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hansjone/auriga/infra/ai-gateway/internal/admin"
	"github.com/hansjone/auriga/infra/ai-gateway/internal/budget"
	"github.com/hansjone/auriga/infra/ai-gateway/internal/modelprotocol"
	"github.com/hansjone/auriga/infra/ai-gateway/internal/relay"
	"github.com/hansjone/auriga/infra/ai-gateway/internal/relaysupervisor"
	"github.com/hansjone/auriga/infra/ai-gateway/internal/testpg"
	"github.com/jackc/pgx/v5"
)

type noRecoveryIO struct{ t *testing.T }

type blockedRequestBody struct{ entered, release chan struct{} }

func (b *blockedRequestBody) Read([]byte) (int, error) {
	close(b.entered)
	<-b.release
	return 0, io.ErrUnexpectedEOF
}
func (*blockedRequestBody) Close() error { return nil }

func TestHandlerInFlightAndClosedAdmission(t *testing.T) {
	c := testConfig()
	c.MaxInFlight = 1
	h, e := New(c)
	if e != nil {
		t.Fatal(e)
	}
	body := &blockedRequestBody{entered: make(chan struct{}), release: make(chan struct{})}
	request := httptest.NewRequest("POST", chatPath, body)
	request.Header.Set("Authorization", "Bearer original")
	request.Header.Set("Content-Type", "application/json")
	done := make(chan int, 1)
	go func() { w := &deadlineRecorder{httptest.NewRecorder()}; h.ServeHTTP(w, request); done <- w.Code }()
	<-body.entered
	w := &deadlineRecorder{httptest.NewRecorder()}
	r := httptest.NewRequest("GET", modelsPath, nil)
	r.Header.Set("Authorization", "Bearer original")
	h.ServeHTTP(w, r)
	if w.Code != 429 || !strings.Contains(w.Body.String(), "common.rate_limited") {
		t.Fatal("in-flight limit bypassed", w.Code)
	}
	close(body.release)
	if status := <-done; status != 400 {
		t.Fatal("read failure not rejected", status)
	}
	h.CloseAdmission()
	w = &deadlineRecorder{httptest.NewRecorder()}
	h.ServeHTTP(w, r)
	if w.Code != 503 {
		t.Fatal("closed handler entered control plane", w.Code)
	}
}

func (l noRecoveryIO) GetRecoveryCandidate(context.Context, budget.Ref) (*budget.RecoveryCandidate, error) {
	l.t.Error("active cell must not be read")
	return nil, nil
}

func waitCapacity(t *testing.T, s *relaysupervisor.Supervisor, n int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for s.Stats().Occupied != n {
		select {
		case <-ctx.Done():
			t.Fatalf("capacity not reached: %+v", s.Stats())
		case <-tick.C:
		}
	}
}
func socketSupervisor(t *testing.T, f *fixture) *relaysupervisor.Supervisor {
	t.Helper()
	s, e := relaysupervisor.New(f.http.Ledger, 1, 100*time.Millisecond, 5*time.Millisecond)
	if e != nil {
		t.Fatal(e)
	}
	f.http.ReserveRecovery = s.Reserve
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if e := s.Drain(ctx); e != nil {
			t.Error(e)
		}
	})
	return s
}
func TestRecoveryPGSocketCapacityBeforeHandoff(t *testing.T) {
	f := fixtureNew(t)
	sup := socketSupervisor(t, f)
	entered, release := make(chan struct{}), make(chan struct{})
	policy := f.cfg.Policy
	f.cfg.Policy = func(ctx context.Context, tid, model string, v admin.RelayVersions) (modelprotocol.Policy, error) {
		close(entered)
		<-release
		return policy(ctx, tid, model, v)
	}
	var calls atomic.Int32
	s := f.server(t, finite(completion(textChoice), &calls))
	done := make(chan int, 1)
	go func() {
		req, _ := http.NewRequest("POST", s.URL+chatPath, strings.NewReader(plainRequest))
		req.Header.Set("Authorization", "Bearer "+f.raw)
		req.Header.Set("Content-Type", "application/json")
		r, e := s.Client().Do(req)
		if e != nil {
			done <- 0
			return
		}
		_, _ = io.Copy(io.Discard, r.Body)
		_ = r.Body.Close()
		done <- r.StatusCode
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("no execution reached policy")
	}
	status, _, _ := f.request(t, s, "POST", chatPath, plainRequest, "")
	if status != 503 || calls.Load() != 0 {
		t.Fatal("unhanded capacity allowed second execution", status, calls.Load())
	}
	var requests int
	if e := f.db.Transaction(t.Context(), f.control.tenant, func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(), "SELECT count(*) FROM ai.budget_request WHERE tenant_id=$1", f.control.tenant).Scan(&requests)
	}); e != nil {
		t.Fatal(e)
	}
	if requests != 0 {
		t.Fatal("admission happened before reservation boundary")
	}
	close(release)
	if status := <-done; status != 200 {
		t.Fatal("first request failed", status)
	}
	waitCapacity(t, sup, 0)
	if calls.Load() != 1 {
		t.Fatal("incorrect dispatch count")
	}
}
func TestRecoveryPGSocketDatabaseFailureRetainsResponsibility(t *testing.T) {
	f := fixtureNew(t)
	sup := socketSupervisor(t, f)
	f.denyUsageWrites(t)
	var calls atomic.Int32
	s := f.server(t, finite(completion(textChoice), &calls))
	status, _, b := f.request(t, s, "POST", chatPath, plainRequest, "")
	if status != 503 || !strings.Contains(b, "ai.budget_unavailable") {
		t.Fatal(status, b)
	}
	waitCapacity(t, sup, 1)
	status, _, _ = f.request(t, s, "POST", chatPath, plainRequest, "")
	if status != 503 || calls.Load() != 1 {
		t.Fatal("DB failure discarded pending or redispatched")
	}
	// Closing new admission must keep the existing worker alive until its drain.
	sup.CloseAdmission()
	var role string
	if e := f.db.Transaction(t.Context(), f.control.tenant, func(tx pgx.Tx) error { return tx.QueryRow(t.Context(), "SELECT current_user").Scan(&role) }); e != nil {
		t.Fatal(e)
	}
	conn, e := pgx.Connect(t.Context(), testpg.DSN(t))
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = conn.Close(context.Background()) }()
	if e = usageCatalogACL(t.Context(), conn, "GRANT INSERT ON ai.budget_usage TO "+pgx.Identifier{role}.Sanitize()); e != nil {
		t.Fatal(e)
	}
	waitCapacity(t, sup, 0)
	f.assertState(t, "unknown", 0, 48)
	var usage, outbox int
	if e = f.db.Transaction(t.Context(), f.control.tenant, func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM ai.budget_usage WHERE tenant_id=$1),(SELECT count(*) FROM ai.budget_outbox WHERE tenant_id=$1)`, f.control.tenant).Scan(&usage, &outbox)
	}); e != nil {
		t.Fatal(e)
	}
	if usage != 1 || outbox != 1 {
		t.Fatal("duplicate recovery accounting", usage, outbox)
	}
}

func TestRecoveryPGSocketDisconnectDoesNotProveStop(t *testing.T) {
	f := fixtureNew(t)
	sup := socketSupervisor(t, f)
	proof, ended := make(chan struct{}), make(chan struct{})
	s := f.server(t, func(ctx context.Context, _ relay.Submission, _ []byte) (relay.Exchange, error) {
		pr, pw := io.Pipe()
		go func() { _, _ = io.WriteString(pw, firstChunk); <-ctx.Done(); _ = pw.Close(); close(ended) }()
		return relay.Exchange{Body: pr, Stopped: proof}, nil
	})
	c := socket(t, s)
	body := strings.Replace(plainRequest, `"max_tokens":`, `"stream":true,"max_tokens":`, 1)
	_, e := io.WriteString(c, "POST "+chatPath+" HTTP/1.1\r\nHost: example.com\r\nAuthorization: Bearer "+f.raw+"\r\nContent-Type: application/json\r\nContent-Length: "+fmt.Sprint(len(body))+"\r\n\r\n"+body)
	if e != nil {
		t.Fatal(e)
	}
	r, e := http.ReadResponse(bufio.NewReader(c), nil)
	if e != nil {
		t.Fatal(e)
	}
	_ = c.Close()
	_ = r.Body.Close()
	select {
	case <-ended:
	case <-time.After(time.Second):
		t.Fatal("disconnect did not cancel transport")
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for sup.Stats().Pending != 1 {
		select {
		case <-ctx.Done():
			t.Fatal("pending handoff lost on disconnect")
		case <-tick.C:
		}
	}
	f.assertState(t, "unknown", 1, 48)
	close(proof)
	waitCapacity(t, sup, 0)
	f.assertState(t, "unknown", 0, 48)
}
func (l noRecoveryIO) Recover(context.Context, budget.Ref, int64, budget.RecoveryEvidence) (*budget.Reservation, error) {
	l.t.Error("active cell must not be recovered")
	return nil, nil
}
func TestFullRecoveryCapacityRejectsBeforeExecute(t *testing.T) {
	s, e := relaysupervisor.New(noRecoveryIO{t}, 1, time.Millisecond, 10*time.Millisecond)
	if e != nil {
		t.Fatal(e)
	}
	f, ok := s.Reserve()
	if !ok {
		t.Fatal("first reservation")
	}
	defer func() {
		f(nil)
		if e := s.Drain(t.Context()); e != nil {
			t.Error(e)
		}
	}()
	c := testConfig()
	c.ReserveRecovery = s.Reserve
	h, e := New(c)
	if e != nil {
		t.Fatal(e)
	}
	r := httptest.NewRequest("POST", chatPath, strings.NewReader(`{"model":"anything"}`))
	r.Header.Set("Authorization", "Bearer original")
	r.Header.Set("Content-Type", "application/json")
	w := &deadlineRecorder{httptest.NewRecorder()}
	h.ServeHTTP(w, r)
	// Core/Gate/Ledger are uninitialized sentinels: any Execute/Admit/Transport
	// access panics. A well-formed request must instead fail at reservation.
	if w.Code != 503 || !strings.Contains(w.Body.String(), "ai.budget_unavailable") {
		t.Fatalf("capacity bypassed: %d %s", w.Code, w.Body.String())
	}
}
