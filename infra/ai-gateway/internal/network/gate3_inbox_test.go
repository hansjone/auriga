package network

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
)

// Gate 3: private inboxPrefix keeps request() inside the connection's own
// reply prefix, for mux (default) and noMux (UseOldRequestStyle) requests.
// SDK under test: nats.go v1.49.0 (the Go SDK; nats.js is covered elsewhere).

func connSubs(t *testing.T, h *harness, ops *nats.Conn, cid uint64) []string {
	t.Helper()
	ci, err := LookupConn(ctxT(t), ops, h.srv.ID(), cid)
	if err != nil {
		t.Fatal(err)
	}
	return ci.Subs
}

func cidOf(t *testing.T, c *client) uint64 {
	t.Helper()
	cid, err := c.nc.GetClientID()
	if err != nil {
		t.Fatal(err)
	}
	return cid
}

// eventuallySubs polls CONNZ until the connection has want subscriptions
// and returns how long that took; it fails after 2s.
func eventuallySubs(t *testing.T, h *harness, ops *nats.Conn, cid uint64, want int) ([]string, time.Duration) {
	t.Helper()
	start := time.Now()
	for {
		subs := connSubs(t, h, ops, cid)
		if len(subs) == want {
			return subs, time.Since(start)
		}
		if time.Since(start) > 2*time.Second {
			t.Fatalf("subs = %v, want %d after 2s", subs, want)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func waitConnGone(t *testing.T, h *harness, ops *nats.Conn, cid uint64) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		_, err := LookupConn(ctxT(t), ops, h.srv.ID(), cid)
		if errors.Is(err, ErrNoSuchConn) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("connection %d still listed: %v", cid, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func assertSubsInPrefix(t *testing.T, subs []string, ingress string, want int, wantTokens int) {
	t.Helper()
	if len(subs) != want {
		t.Fatalf("subs = %v, want %d", subs, want)
	}
	for _, s := range subs {
		rest, ok := strings.CutPrefix(s, ReplyPrefix(ingress)+".")
		if !ok || len(strings.Split(rest, ".")) != wantTokens {
			t.Fatalf("subscription %q outside private prefix shape", s)
		}
	}
}

func TestGate3_PrivateInboxPrefix_Mux(t *testing.T) {
	h := newHarness(t, defaultBrokerOpts())
	ops := h.opsConn()
	ad := h.startAdapter()
	c := h.connectNew(time.Now().Add(time.Hour))
	cid := cidOf(t, c)

	m, err := c.nc.Request(UpSubject(c.ingress), []byte("hello"), 2*time.Second)
	if err != nil || string(m.Data) != "ack:"+c.ingress {
		t.Fatalf("request: %v", err)
	}
	u := ad.expectUp(t)
	if !ValidReplyFor(c.ingress, u.reply) {
		t.Fatalf("mux reply %q not in private prefix", u.reply)
	}
	subs := connSubs(t, h, ops, cid)
	assertSubsInPrefix(t, subs, c.ingress, 1, 2) // <prefix>.<nuid>.*
	if !strings.HasSuffix(subs[0], ".*") {
		t.Fatalf("mux sub %q", subs[0])
	}

	// Concurrent requests share the single mux subscription.
	var wg sync.WaitGroup
	errs := make(chan error, 5)
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := c.nc.Request(UpSubject(c.ingress), []byte("c"), 2*time.Second); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		ad.expectUp(t)
	}

	// Timeout: adapter withholds the answer; the late answer is dropped.
	ad.mu.Lock()
	ad.noRespond["slow"] = true
	ad.mu.Unlock()
	if _, err := c.nc.Request(UpSubject(c.ingress), []byte("slow"), 300*time.Millisecond); !errors.Is(err, nats.ErrTimeout) {
		t.Fatalf("slow request err = %v", err)
	}
	late := ad.expectUp(t)
	if err := ad.nc.Publish(late.reply, []byte("late")); err != nil {
		t.Fatal(err)
	}
	_ = ad.nc.Flush()
	time.Sleep(100 * time.Millisecond)
	c.noAsyncErr(t)
	assertSubsInPrefix(t, connSubs(t, h, ops, cid), c.ingress, 1, 2)
	t.Logf("mux subscription: %s", subs[0])
}

func TestGate3_PrivateInboxPrefix_NoMux(t *testing.T) {
	h := newHarness(t, defaultBrokerOpts())
	ops := h.opsConn()
	ad := h.startAdapter()
	c := h.connectNew(time.Now().Add(time.Hour), nats.UseOldRequestStyle())
	cid := cidOf(t, c)

	// While a request is pending exactly one <prefix>.<nuid> subscription
	// exists; after the timeout the SDK sends UNSUB and it is removed.
	ad.mu.Lock()
	ad.noRespond["slow"] = true
	ad.mu.Unlock()
	done := make(chan error, 1)
	go func() {
		_, err := c.nc.Request(UpSubject(c.ingress), []byte("slow"), 800*time.Millisecond)
		done <- err
	}()
	pending := ad.expectUp(t)
	if !ValidReplyFor(c.ingress, pending.reply) || strings.Contains(strings.TrimPrefix(pending.reply, ReplyPrefix(c.ingress)+"."), ".") {
		t.Fatalf("noMux reply %q", pending.reply)
	}
	assertSubsInPrefix(t, connSubs(t, h, ops, cid), c.ingress, 1, 1)
	if err := <-done; !errors.Is(err, nats.ErrTimeout) {
		t.Fatalf("slow request err = %v", err)
	}
	_ = c.nc.Flush()
	_, took := eventuallySubs(t, h, ops, cid, 0)
	t.Logf("noMux inbox removed %v after the timeout", took)
	// A late answer to the cancelled inbox has no interest and goes nowhere.
	_ = ad.nc.Publish(pending.reply, []byte("late"))
	_ = ad.nc.Flush()
	c.noAsyncErr(t)

	// Answered request works and stays inside the prefix (cleanup: see below).
	if m, err := c.nc.Request(UpSubject(c.ingress), []byte("hello"), 2*time.Second); err != nil || string(m.Data) != "ack:"+c.ingress {
		t.Fatalf("request: %v", err)
	}
	assertSubsInPrefix(t, connSubs(t, h, ops, cid), c.ingress, 1, 1)
}

// First failure, kept as evidence: an answered noMux request leaks its inbox
// subscription when the reply crosses a stream import (SVC -> CLIENTS).
// nats.go sends SUB, then UNSUB <sid> 1 (AutoUnsubscribe). The server's
// shadow subscription in the exporting account is a copy made at SUB time
// (client.go:3184 "nsub := *sub"); UNSUB max only updates the original
// (client.go:3405), and delivery goes through the copy, so the auto-unsubscribe
// in deliverMsg (client.go:3722-3740) never fires. nats.go then considers the
// subscription closed and its deferred Unsubscribe sends nothing.
// With the account max_subscriptions=8 the 9th request fails.
func TestGate3_NoMuxLeaksAcrossStreamImport_FirstFailure(t *testing.T) {
	h := newHarness(t, defaultBrokerOpts())
	ops := h.opsConn()
	h.startAdapter()
	c := h.connectNew(time.Now().Add(time.Hour), nats.UseOldRequestStyle())
	cid := cidOf(t, c)
	for i := 1; i <= 8; i++ {
		if _, err := c.nc.Request(UpSubject(c.ingress), []byte("n"), 2*time.Second); err != nil {
			t.Fatalf("request %d: %v", i, err)
		}
	}
	time.Sleep(200 * time.Millisecond)
	subs := connSubs(t, h, ops, cid)
	if len(subs) == 0 {
		t.Fatal("upstream fixed: answered noMux inboxes are cleaned up")
	}
	assertSubsInPrefix(t, subs, c.ingress, 8, 1)
	_, err := c.nc.Request(UpSubject(c.ingress), []byte("n"), 500*time.Millisecond)
	if err == nil {
		t.Fatal("9th noMux request succeeded despite 8 leaked subscriptions")
	}
	c.expectAsyncErr(t, "maximum subscriptions exceeded")
	t.Logf("CONFIRMED: 8 answered noMux requests leaked 8 subscriptions; 9th request: %v", err)
}

func TestGate3_DefaultInboxDenied(t *testing.T) {
	h := newHarness(t, defaultBrokerOpts())
	h.startAdapter()
	tk, ing := h.tickets.issue(t, time.Now().Add(time.Hour))
	c, err := h.connect(h.wsURL(), tk, "") // no CustomInboxPrefix -> _INBOX.
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.nc.Request(UpSubject(ing), []byte("x"), 500*time.Millisecond)
	if err == nil {
		t.Fatal("request with global _INBOX succeeded")
	}
	c.expectAsyncErr(t, `subscription to "_INBOX.`)
	t.Logf("default _INBOX request: %v", err)
}

func TestGate3_MigrationCleansReplyPrefix(t *testing.T) {
	h := newHarness(t, defaultBrokerOpts())
	ops := h.opsConn()
	ad := h.startAdapter()
	exp := time.Now().Add(time.Hour)
	c1 := h.connectNew(exp)
	cid1 := cidOf(t, c1)
	if _, err := c1.nc.Request(UpSubject(c1.ingress), []byte("one"), 2*time.Second); err != nil {
		t.Fatal(err)
	}
	old := ad.expectUp(t)

	// Plugin supervisor migrates: new ticket, new ingress, then closes the old one.
	c2 := h.connectNew(exp)
	cid2 := cidOf(t, c2)
	c1.nc.Close()
	waitConnGone(t, h, ops, cid1)
	if _, err := c2.nc.Request(UpSubject(c2.ingress), []byte("two"), 2*time.Second); err != nil {
		t.Fatal(err)
	}
	ad.expectUp(t)
	assertSubsInPrefix(t, connSubs(t, h, ops, cid2), c2.ingress, 1, 2)

	// c2 cannot pick up the old prefix ...
	if _, err := c2.nc.SubscribeSync(ReplyPrefix(c1.ingress) + ".>"); err != nil {
		t.Fatal(err)
	}
	_ = c2.nc.Flush()
	c2.expectAsyncErr(t, `subscription to "`+ReplyPrefix(c1.ingress))
	// ... a stale answer to the old prefix reaches nobody ...
	_ = ad.nc.Publish(old.reply, []byte("stale"))
	// ... and a request on the new ingress that names the old prefix is refused.
	if err := c2.nc.PublishRequest(UpSubject(c2.ingress), old.reply, []byte("x")); err != nil {
		t.Fatal(err)
	}
	ad.expectUp(t)
	select {
	case r := <-ad.rejected:
		if r.reply != old.reply {
			t.Fatalf("rejected %q", r.reply)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("adapter answered old-ingress reply")
	}
}
