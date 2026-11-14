package network_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hansjone/auriga/infra/ai-gateway/internal/modelgate"
	"github.com/hansjone/auriga/infra/ai-gateway/internal/network"
	"github.com/hansjone/auriga/infra/ai-gateway/internal/sharedauth"
)

// W/P and msgnet-v2 authority doubles. No PG/DSH process or NAS is used.
type sharedFixture struct {
	mu             sync.Mutex
	sessions       map[string]sharedauth.Binding
	task           string
	desktopAllowed bool
}

func (f *sharedFixture) Resolve(_ context.Context, q sharedauth.Lookup) (sharedauth.Snapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	b, ok := f.sessions[q.SessionID]
	scopes := []string{"model.invoke"}
	if q.Audience == sharedauth.Message {
		scopes = []string{"message.send", "message.receive"}
	}
	return sharedauth.Snapshot{Binding: b, CredentialVersion: 1, Active: ok && q.TaskID == f.task, Scopes: scopes}, nil
}
func (f *sharedFixture) Authorize(_ context.Context, source sharedauth.Binding, target network.SharedTarget) (sharedauth.Binding, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.sessions[source.SessionID] != source || source.TenantID != target.TenantID {
		return sharedauth.Binding{}, sharedauth.ErrRevoked
	}
	if target.DesktopNode != "" {
		if f.desktopAllowed {
			return sharedauth.Binding{}, nil
		}
		return sharedauth.Binding{}, sharedauth.ErrRevoked
	}
	b, ok := f.sessions[target.SessionID]
	if !ok || target.TaskID != f.task {
		return sharedauth.Binding{}, sharedauth.ErrRevoked
	}
	return b, nil
}
func (f *sharedFixture) AuthorizeDesktop(_ context.Context, source network.NodeIdentity, local string, target network.SharedTarget) (sharedauth.Binding, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	b, ok := f.sessions[target.SessionID]
	if !f.desktopAllowed || !ok || source.TenantID != b.TenantID || target.TaskID != f.task || local != "private" {
		return sharedauth.Binding{}, sharedauth.ErrRevoked
	}
	return b, nil
}
func fixture(t *testing.T) (*sharedFixture, *sharedauth.Gate, func(sharedauth.Binding, string) string, sharedauth.Binding, sharedauth.Binding) {
	t.Helper()
	tenant, pool := uuid.NewString(), uuid.NewString()
	binding := func() sharedauth.Binding {
		return sharedauth.Binding{TenantID: tenant, MemberID: uuid.NewString(), PlacementID: uuid.NewString(), PhysicalInstanceID: pool, Mode: "shared", Epoch: 1, Revision: 1, SessionID: uuid.NewString(), PlatformSessionID: uuid.NewString(), AuthGeneration: 1}
	}
	a, b := binding(), binding()
	f := &sharedFixture{sessions: map[string]sharedauth.Binding{a.SessionID: a, b.SessionID: b}, task: uuid.NewString(), desktopAllowed: true}
	pub, key, _ := ed25519.GenerateKey(rand.Reader)
	g, e := sharedauth.New(f, map[string]ed25519.PublicKey{"runtime": pub}, time.Now)
	if e != nil {
		t.Fatal(e)
	}
	issue := func(b sharedauth.Binding, aud string) string {
		scopes := []string{"model.invoke"}
		if aud == sharedauth.Message {
			scopes = []string{"message.send", "message.receive"}
		}
		tok, e := g.Issue(context.Background(), sharedauth.Lookup{TenantID: b.TenantID, SessionID: b.SessionID, TaskID: f.task, Audience: aud}, scopes, time.Minute, "runtime", key)
		if e != nil {
			t.Fatal(e)
		}
		return tok
	}
	return f, g, issue, a, b
}

type testLedger struct {
	mu    sync.Mutex
	seen  map[string][32]byte
	usage map[string]int64
	calls int
}

func (l *testLedger) Begin(_ context.Context, b sharedauth.Binding, id string, fp [32]byte) (bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	k := b.MemberID + id
	if old, ok := l.seen[k]; ok {
		if old != fp {
			return false, sharedauth.ErrBinding
		}
		return false, nil
	}
	l.seen[k] = fp
	return true, nil
}
func (l *testLedger) Finish(_ context.Context, b sharedauth.Binding, _ string, u modelgate.SharedUsage, _ error) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.usage[b.MemberID] += u.OutputTokens
	l.calls++
	return nil
}

type relayFunc func(context.Context, modelgate.SharedCall, func([]byte) error) (modelgate.SharedUsage, error)

func (f relayFunc) Stream(c context.Context, q modelgate.SharedCall, emit func([]byte) error) (modelgate.SharedUsage, error) {
	return f(c, q, emit)
}
func TestSharedModelsConcurrentMeteringAndNoReplay(t *testing.T) {
	f, g, issue, a, b := fixture(t)
	ledger := &testLedger{seen: map[string][32]byte{}, usage: map[string]int64{}}
	ready := make(chan struct{}, 2)
	release := make(chan struct{})
	relay := relayFunc(func(ctx context.Context, q modelgate.SharedCall, emit func([]byte) error) (modelgate.SharedUsage, error) {
		ready <- struct{}{}
		select {
		case <-release:
		case <-ctx.Done():
			return modelgate.SharedUsage{}, ctx.Err()
		}
		n := int64(3)
		if q.Binding.MemberID == b.MemberID {
			n = 7
		}
		e := emit([]byte(q.Binding.MemberID))
		return modelgate.SharedUsage{OutputTokens: n, Known: true}, e
	})
	p, _ := modelgate.NewSharedProvider(g, relay, ledger)
	requests := []modelgate.SharedRequest{}
	for _, who := range []sharedauth.Binding{a, b} {
		requests = append(requests, modelgate.SharedRequest{TenantID: who.TenantID, SessionID: who.SessionID, TaskID: f.task, CallID: uuid.NewString(), Capability: issue(who, sharedauth.Model), Body: []byte(`{"model":"test"}`)})
	}
	errs := make(chan error, 2)
	for _, q := range requests {
		go func() {
			errs <- p.Invoke(context.Background(), q, func(chunk []byte) error {
				if string(chunk) != a.MemberID && string(chunk) != b.MemberID {
					t.Error("wrong actor")
				}
				return nil
			})
		}()
	}
	for i := 0; i < 2; i++ {
		select {
		case <-ready:
		case <-time.After(5 * time.Second):
			t.Fatal("not concurrent")
		}
	}
	close(release)
	for i := 0; i < 2; i++ {
		if e := <-errs; e != nil {
			t.Fatal(e)
		}
	}
	if ledger.usage[a.MemberID] != 3 || ledger.usage[b.MemberID] != 7 {
		t.Fatal(ledger.usage)
	}
	if e := p.Invoke(context.Background(), requests[0], func([]byte) error { return nil }); e == nil {
		t.Fatal("duplicate invoked")
	}
	if ledger.calls != 2 {
		t.Fatal("duplicate metering")
	}
	forged := requests[0]
	forged.SessionID = b.SessionID
	if e := p.Invoke(context.Background(), forged, func([]byte) error { return nil }); e == nil {
		t.Fatal("forged session")
	}
}
func TestSharedStreamRevocationCancelsAndDropsLateChunk(t *testing.T) {
	f, g, issue, a, _ := fixture(t)
	ledger := &testLedger{seen: map[string][32]byte{}, usage: map[string]int64{}}
	tok := issue(a, sharedauth.Model)
	started := make(chan struct{})
	late := make(chan error, 1)
	relay := relayFunc(func(ctx context.Context, _ modelgate.SharedCall, emit func([]byte) error) (modelgate.SharedUsage, error) {
		close(started)
		<-ctx.Done()
		late <- emit([]byte("late"))
		return modelgate.SharedUsage{Known: false}, ctx.Err()
	})
	p, _ := modelgate.NewSharedProvider(g, relay, ledger)
	done := make(chan error, 1)
	go func() {
		done <- p.Invoke(context.Background(), modelgate.SharedRequest{TenantID: a.TenantID, SessionID: a.SessionID, TaskID: f.task, CallID: uuid.NewString(), Capability: tok, Body: []byte("prompt")}, func([]byte) error { t.Error("late chunk delivered"); return nil })
	}()
	<-started
	f.mu.Lock()
	delete(f.sessions, a.SessionID)
	f.mu.Unlock()
	select {
	case e := <-done:
		if e == nil {
			t.Fatal("revoke success")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cooperative cancel timed out")
	}
	if <-late == nil || ledger.calls != 1 {
		t.Fatal("late/settlement")
	}
}

type transportDouble struct {
	mu    sync.Mutex
	items []network.SharedDelivery
	fail  bool
}

func (d *transportDouble) Deliver(_ context.Context, q network.SharedDelivery) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.items = append(d.items, q)
	if d.fail {
		return errors.New("ambiguous transport")
	}
	return nil
}
func TestSharedMailboxesPrivateDedupBackpressureAndSwitch(t *testing.T) {
	f, g, issue, a, b := fixture(t)
	transport := &transportDouble{}
	broker, _ := network.NewSharedBroker(g, f, transport, time.Now, 2)
	send, receive := issue(a, sharedauth.Message), issue(b, sharedauth.Message)
	target := network.SharedTarget{TenantID: b.TenantID, SessionID: b.SessionID, TaskID: f.task}
	id := uuid.NewString()
	enqueue := func(id string, target network.SharedTarget) error {
		return broker.Enqueue(context.Background(), send, a.TenantID, a.SessionID, f.task, id, target, []byte("command"))
	}
	if e := enqueue(id, target); e != nil {
		t.Fatal(e)
	}
	if e := enqueue(id, target); e != nil {
		t.Fatal(e)
	}
	desktop := network.SharedTarget{TenantID: a.TenantID, DesktopNode: "desktop", DesktopLocal: "private"}
	if e := enqueue(uuid.NewString(), desktop); e != nil {
		t.Fatal(e)
	}
	if e := enqueue(uuid.NewString(), target); e == nil {
		t.Fatal("unbounded queue")
	}
	if e := broker.Flush(context.Background()); e != nil {
		t.Fatal(e)
	}
	if len(transport.items) != 2 {
		t.Fatal("broadcast or lost delivery")
	}
	var cloud network.SharedDelivery
	for _, d := range transport.items {
		if d.Target.SessionID != "" {
			cloud = d
			if d.Recipient != b {
				t.Fatal("wrong recipient")
			}
		}
	}
	count := 0
	consume := func(context.Context, []byte) error { count++; return errors.New("side effect may have occurred") }
	if e := broker.Receive(context.Background(), receive, b.TenantID, b.SessionID, f.task, cloud, consume); e == nil {
		t.Fatal("failure lost")
	}
	_ = broker.Receive(context.Background(), receive, b.TenantID, b.SessionID, f.task, cloud, consume)
	if count != 1 {
		t.Fatal("side effect replay")
	}
	fromDesktop := cloud
	fromDesktop.Sender = sharedauth.Binding{}
	fromDesktop.MessageID = uuid.NewString()
	node := network.NodeIdentity{TenantID: b.TenantID, NodeID: "desktop", Incarnation: "boot", Epoch: 1}
	if e := broker.ReceiveDesktop(context.Background(), node, "private", receive, b.TenantID, b.SessionID, f.task, fromDesktop, func(context.Context, []byte) error { count++; return nil }); e != nil {
		t.Fatal(e)
	}
	if count != 2 {
		t.Fatal("desktop to cloud missing")
	}
	if e := broker.Receive(context.Background(), send, a.TenantID, a.SessionID, f.task, cloud, consume); e == nil {
		t.Fatal("private stream leaked")
	}
	// A new admission queue contains a message bound to B's old epoch.
	broker, _ = network.NewSharedBroker(g, f, transport, time.Now, 2)
	if e := broker.Enqueue(context.Background(), send, a.TenantID, a.SessionID, f.task, uuid.NewString(), target, []byte("old")); e != nil {
		t.Fatal(e)
	}
	f.mu.Lock()
	next := b
	next.Epoch++
	f.sessions[b.SessionID] = next
	f.mu.Unlock()
	if e := broker.Flush(context.Background()); e == nil {
		t.Fatal("old epoch delivered")
	}
	if len(transport.items) != 2 {
		t.Fatal("old queue entered new session")
	}
	_ = broker.Flush(context.Background())
	if len(transport.items) != 2 {
		t.Fatal("failure replay")
	}
	// Desktop ACL is live, never inherited from the shared owner.
	f.mu.Lock()
	f.desktopAllowed = false
	f.mu.Unlock()
	if e := enqueue(uuid.NewString(), desktop); e == nil {
		t.Fatal("desktop ACL bypass")
	}
}
