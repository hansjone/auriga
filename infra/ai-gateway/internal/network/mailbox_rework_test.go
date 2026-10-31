package network

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
)

func readyMailboxFixture(t *testing.T, f *fakeMailboxRPC) (*Runtime, *nats.Subscription, liveBinding, RuntimeConfig) {
	t.Helper()
	h, cfg, _ := runtimeFixture(t)
	cfg.Collab = startMailboxRPC(t, f)
	run, err := StartRuntime(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(run.Close)
	nc, err := nats.Connect(h.wsURL(), nats.Token(connectTicket), nats.NoReconnect())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(nc.Close)
	b := run.redeemer.snapshot()[0]
	b.Epoch = 42
	b.Incarnation = "inc-1"
	b.ChannelNonce = "nonce-1"
	run.redeemer.mu.Lock()
	run.redeemer.bindings[b.Ingress] = b
	run.redeemer.mu.Unlock()
	sub, err := nc.SubscribeSync(DownSubject(b.Ingress))
	if err != nil {
		t.Fatal(err)
	}
	if err = nc.Flush(); err != nil {
		t.Fatal(err)
	}
	return run, sub, b, cfg
}

type countingPublisher struct {
	delegate mailboxPublisher
	mu       sync.Mutex
	counts   map[string]int
}

func (p *countingPublisher) Publish(subject string, data []byte) error {
	p.mu.Lock()
	p.counts[subject]++
	p.mu.Unlock()
	return p.delegate.Publish(subject, data)
}

// Runtime.Close joins admitted workers, but NATS Close/Unsubscribe can return
// while a subscription dispatcher is still returning from receive. Join those
// dispatchers too before the test ends (or reads its final publication counts).
func mailboxFixtureCloser(t *testing.T, run *Runtime) func() {
	t.Helper()
	run.mailbox.mu.Lock()
	subs := append([]*nats.Subscription(nil), run.mailbox.subscriptions...)
	for _, sub := range run.mailbox.upSubscriptions {
		subs = append(subs, sub)
	}
	run.mailbox.mu.Unlock()
	done := make([]chan struct{}, len(subs))
	for i, sub := range subs {
		done[i] = make(chan struct{})
		sub.SetClosedHandler(func(string) { close(done[i]) })
	}
	closeFixture := sync.OnceFunc(func() {
		defer run.Close()
		// Drain preserves queued flood messages. Closing the connection here
		// would discard them and weaken the final diagnostic-count assertion.
		for _, sub := range subs {
			if err := sub.Drain(); err != nil && !errors.Is(err, nats.ErrBadSubscription) && !errors.Is(err, nats.ErrConnectionClosed) {
				t.Errorf("drain mailbox subscription %s: %v", sub.Subject, err)
			}
		}
		guard := time.NewTimer(5 * time.Second)
		defer guard.Stop()
		for i, exited := range done {
			select {
			case <-exited:
			case <-guard.C:
				t.Errorf("mailbox callback did not exit: %s", subs[i].Subject)
				return
			}
		}
		// No dispatcher can start another worker after all close notifications.
		run.mailbox.workers.Wait()
	})
	t.Cleanup(closeFixture)
	return closeFixture
}

func TestMailboxFixtureCloseJoinsSubscriptionCallback(t *testing.T) {
	h, cfg, _ := runtimeFixture(t)
	run, err := StartRuntime(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(run.Close)
	nc, err := nats.Connect(h.wsURL(), nats.Token(connectTicket), nats.NoReconnect())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(nc.Close)
	b := run.redeemer.snapshot()[0]
	entered, release := make(chan struct{}), make(chan struct{})
	unblock := sync.OnceFunc(func() { close(release) })
	defer unblock()
	// A second real dispatcher on the same authenticated ingress makes the
	// Close-vs-callback ordering deterministic, independently of worker timing.
	sub, err := run.data.Subscribe(UpSubject(b.Ingress), func(*nats.Msg) {
		close(entered)
		<-release
	})
	if err != nil {
		t.Fatal(err)
	}
	run.mailbox.mu.Lock()
	run.mailbox.subscriptions = append(run.mailbox.subscriptions, sub)
	run.mailbox.mu.Unlock()
	if err = run.data.FlushTimeout(time.Second); err != nil {
		t.Fatal(err)
	}
	closeFixture := mailboxFixtureCloser(t, run)
	if err = nc.Publish(UpSubject(b.Ingress), []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("subscription callback never entered")
	}
	// Runtime.Close itself does not join the held dispatcher.
	run.Close()
	closed := make(chan struct{})
	go func() { closeFixture(); close(closed) }()
	defer func() { unblock(); <-closed }()
	select {
	case <-closed:
		t.Fatal("fixture closed before subscription callback exited")
	case <-time.After(20 * time.Millisecond):
		// A negative observation window, after callback entry and runtime close.
	}
	unblock()
}

func TestBrokerIngressFloodDoesNotStarveAnotherNode(t *testing.T) {
	h, cfg, authority := runtimeFixture(t)
	backend := &fakeMailboxRPC{}
	cfg.Collab = startMailboxRPC(t, backend)
	run, err := StartRuntime(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(run.Close)
	// Connection setup is outside the fairness observation. This fixture's
	// 100ms reconciliation budget is not a scheduling budget for mailbox RPCs.
	connectCtx, cancelConnect := context.WithTimeout(t.Context(), registrationPhaseGuard)
	err = awaitRegistrationRPC(connectCtx, run.collab)
	cancelConnect()
	if err != nil {
		t.Fatal("collab connection before flood:", err)
	}
	run.mailbox.client.timeout = time.Second
	first, err := nats.Connect(h.wsURL(), nats.Token(connectTicket), nats.NoReconnect())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(first.Close)
	// The fixture's single-ticket latch is reset for a different, fresh ticket.
	authority.mu.Lock()
	authority.used = false
	authority.mu.Unlock()
	ticket := strings.Replace(connectTicket, `"node":"1"`, `"node":"2"`, 1)
	ticket = strings.Replace(ticket, strings.Repeat("A", 43), strings.Repeat("B", 43), 1)
	second, err := nats.Connect(h.wsURL(), nats.Token(ticket), nats.NoReconnect())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(second.Close)
	ingressA, ingressB := ingressForTicket(strings.Repeat("A", 43)), ingressForTicket(strings.Repeat("B", 43))
	run.redeemer.mu.Lock()
	a, b := run.redeemer.bindings[ingressA], run.redeemer.bindings[ingressB]
	a.Epoch, b.Epoch = 42, 42
	a.Incarnation, b.Incarnation = "inc-1", "inc-2"
	a.ChannelNonce, b.ChannelNonce = "nonce-1", "nonce-2"
	run.redeemer.bindings[ingressA], run.redeemer.bindings[ingressB] = a, b
	run.redeemer.mu.Unlock()
	response := signedMailboxFrame(t, cfg.ProtocolSigning, "fetch", b, "req-1")
	backend.mu.Lock()
	backend.response = response
	backend.mu.Unlock()
	published := &countingPublisher{delegate: run.mailbox.publisher, counts: make(map[string]int)}
	run.mailbox.publisher = published
	sub, err := second.SubscribeSync(DownSubject(ingressB))
	if err != nil {
		t.Fatal(err)
	}
	if err = second.Flush(); err != nil {
		t.Fatal(err)
	}
	closeFixture := mailboxFixtureCloser(t, run)
	for i := 0; i < 1024; i++ {
		if err = first.Publish(UpSubject(ingressA), []byte(validMailboxFetch)); err != nil {
			t.Fatal(err)
		}
	}
	if err = second.Publish(UpSubject(ingressB), []byte(validMailboxFetch)); err != nil {
		t.Fatal(err)
	}
	msg, err := sub.NextMsg(registrationPhaseGuard)
	if err != nil || string(msg.Data) != string(response) {
		t.Fatalf("healthy node starved: %v %v", msg, err)
	}
	if err = first.Flush(); err != nil {
		t.Fatal(err)
	}
	closeFixture()
	published.mu.Lock()
	count := published.counts[DownSubject(ingressA)]
	published.mu.Unlock()
	if count > 5 {
		t.Fatalf("flood generated unbounded signed diagnostics: %d", count)
	}
}

func TestMailboxFloodRateAndTenantAdmissionLeaveRoomForOthers(t *testing.T) {
	f := &fakeMailboxRPC{block: true, entered: make(chan struct{}, mailboxWorkers)}
	run, _, b, _ := readyMailboxFixture(t, f)
	// The subscription queue belongs to this authenticated connection, never all nodes.
	if sub := run.mailbox.upSubscriptions[b.Ingress]; sub == nil || sub.Subject != UpSubject(b.Ingress) {
		t.Fatal("no isolated exact ingress subscription")
	}
	for i := 0; i < 256; i++ {
		run.mailbox.receive(&nats.Msg{Subject: UpSubject(b.Ingress), Data: []byte(validMailboxFetch)})
	}
	for i := 1; i <= 40; i++ {
		peer := b
		peer.Node = fmt.Sprint(i + 1)
		peer.Ingress = ingressForTicket(fmt.Sprint(i))
		run.redeemer.mu.Lock()
		run.redeemer.bindings[peer.Ingress] = peer
		run.redeemer.mu.Unlock()
		run.mailbox.receive(&nats.Msg{Subject: UpSubject(peer.Ingress), Data: []byte(validMailboxFetch)})
	}
	run.mailbox.mu.Lock()
	active := run.mailbox.tenantActive[b.Tenant]
	run.mailbox.mu.Unlock()
	if active > 16 {
		t.Fatalf("tenant consumed global capacity: %d", active)
	}
	other := b
	other.Tenant = testDevice
	other.Node = "1000"
	other.Ingress = ingressForTicket("another tenant")
	run.redeemer.mu.Lock()
	run.redeemer.bindings[other.Ingress] = other
	run.redeemer.mu.Unlock()
	run.mailbox.receive(&nats.Msg{Subject: UpSubject(other.Ingress), Data: []byte(validMailboxFetch)})
	run.mailbox.mu.Lock()
	admitted := run.mailbox.tenantActive[other.Tenant]
	run.mailbox.mu.Unlock()
	if admitted != 1 {
		t.Fatal("flooding tenant excluded a healthy tenant")
	}
}

func TestHeadHintCoalescesDuringFetchWithoutClientErrors(t *testing.T) {
	f := &fakeMailboxRPC{entered: make(chan struct{}, 1), release: make(chan struct{})}
	run, sub, b, cfg := readyMailboxFixture(t, f)
	fetch := signedMailboxFrame(t, cfg.ProtocolSigning, "fetch", b, "req-1")
	head := signedMailboxFrame(t, cfg.ProtocolSigning, "head", b, "")
	f.mu.Lock()
	f.response = fetch
	f.mu.Unlock()
	run.mailbox.receive(&nats.Msg{Subject: UpSubject(b.Ingress), Data: []byte(validMailboxFetch)})
	select {
	case <-f.entered:
	case <-time.After(time.Second):
		t.Fatal("FETCH did not start")
	}
	for i := 0; i < 100; i++ {
		run.mailbox.receive(&nats.Msg{Subject: headHintRoot + b.Ingress})
	}
	if msg, err := sub.NextMsg(20 * time.Millisecond); err == nil {
		t.Fatalf("hint produced unsolicited client frame: %s", msg.Data)
	}
	f.mu.Lock()
	f.response = head
	f.mu.Unlock()
	close(f.release)
	for _, want := range [][]byte{fetch, head} {
		msg, err := sub.NextMsg(time.Second)
		if err != nil || string(msg.Data) != string(want) {
			t.Fatalf("got %v err=%v", msg, err)
		}
	}
	run.mailbox.workers.Wait()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.calls) != 2 || f.calls[1] != "head" {
		t.Fatalf("unmerged hints: %v", f.calls)
	}
}

func TestHeadBeforeReadyIsSilent(t *testing.T) {
	f := &fakeMailboxRPC{}
	run, sub, b, _ := readyMailboxFixture(t, f)
	run.redeemer.mu.Lock()
	b.Epoch = 0
	run.redeemer.bindings[b.Ingress] = b
	run.redeemer.mu.Unlock()
	run.mailbox.receive(&nats.Msg{Subject: headHintRoot + b.Ingress})
	if msg, err := sub.NextMsg(20 * time.Millisecond); err == nil {
		t.Fatalf("unready hint produced %s", msg.Data)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.calls) != 0 {
		t.Fatal("unready hint called collab")
	}
}

func TestHeadBackendErrorIsSilent(t *testing.T) {
	f := &fakeMailboxRPC{}
	run, sub, b, cfg := readyMailboxFixture(t, f)
	signer, err := newErrorSigner(cfg.ProtocolSigning)
	if err != nil {
		t.Fatal(err)
	}
	defer clear(signer.key)
	raw, err := signer.encode(b, "", &AppError{Code: "network.overloaded", Scope: "request", Retryable: true})
	if err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.response = raw
	f.mu.Unlock()
	run.mailbox.receive(&nats.Msg{Subject: headHintRoot + b.Ingress})
	run.mailbox.workers.Wait()
	if msg, err := sub.NextMsg(20 * time.Millisecond); err == nil {
		t.Fatalf("HEAD failure leaked to client: %s", msg.Data)
	}
}

func TestIngressSubscriptionAndBudgetsAreReclaimed(t *testing.T) {
	f := &fakeMailboxRPC{}
	run, _, b, _ := readyMailboxFixture(t, f)
	run.mailbox.receive(&nats.Msg{Subject: UpSubject(b.Ingress), Data: []byte(validMailboxFetch)})
	run.mailbox.workers.Wait()
	sub := run.mailbox.upSubscriptions[b.Ingress]
	run.redeemer.forget(b.Ingress)
	run.mailbox.mu.Lock()
	defer run.mailbox.mu.Unlock()
	if sub.IsValid() || len(run.mailbox.upSubscriptions) != 0 || len(run.mailbox.nodeBudgets) != 0 || len(run.mailbox.tenantBudgets) != 0 || len(run.mailbox.nodeRefs) != 0 || len(run.mailbox.tenantRefs) != 0 {
		t.Fatal("connection resources leaked")
	}
}

type blockedPublisher struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (p *blockedPublisher) Publish(string, []byte) error {
	p.once.Do(func() { close(p.entered) })
	<-p.release
	return nil
}

func TestBlockedMailboxPublicationDoesNotHoldGlobalIdentityLock(t *testing.T) {
	f := &fakeMailboxRPC{}
	run, _, b, cfg := readyMailboxFixture(t, f)
	f.mu.Lock()
	f.response = signedMailboxFrame(t, cfg.ProtocolSigning, "fetch", b, "req-1")
	f.mu.Unlock()
	publisher := &blockedPublisher{entered: make(chan struct{}), release: make(chan struct{})}
	run.mailbox.publisher = publisher
	defer close(publisher.release)
	run.mailbox.receive(&nats.Msg{Subject: UpSubject(b.Ingress), Data: []byte(validMailboxFetch)})
	select {
	case <-publisher.entered:
	case <-time.After(time.Second):
		t.Fatal("publication not reached")
	}
	done := make(chan struct{})
	go func() { run.redeemer.snapshot(); close(done) }()
	select {
	case <-done:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("blocked publish holds global identity lock")
	}
}
