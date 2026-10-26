package network

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	collab "github.com/hansjone/auriga/infra/gen/auriga/collab/v1"
	"github.com/nats-io/nats.go"
)

type sourcePlan struct {
	response []byte
	release  chan struct{}
	entered  chan context.Context
	returned chan struct{}
}

func newSourcePlan(t *testing.T, response []byte) *sourcePlan {
	t.Helper()
	p := &sourcePlan{response: response, release: make(chan struct{}), entered: make(chan context.Context, 1), returned: make(chan struct{})}
	t.Cleanup(func() {
		select {
		case <-p.release:
		default:
			close(p.release)
		}
	})
	return p
}

// Explicit protocol fixture: the handler deliberately ignores cancellation to
// model a committed mutation/late response, never real IAM authority.
type controlledSourceRPC struct {
	*fakeMailboxRPC
	planMu sync.Mutex
	plans  []*sourcePlan
}

func (f *controlledSourceRPC) SourceGet(ctx context.Context, q *collab.NetworkMailboxServiceSourceGetRequest) (*collab.NetworkMailboxServiceSourceGetResponse, error) {
	_, err := f.call(ctx, "source_get", q.Connection, q.FrameJson)
	if err != nil {
		return nil, err
	}
	f.planMu.Lock()
	p := f.plans[0]
	f.plans = f.plans[1:]
	f.planMu.Unlock()
	p.entered <- ctx
	<-p.release
	defer close(p.returned)
	return &collab.NetworkMailboxServiceSourceGetResponse{FrameJson: p.response}, nil
}

func sourceEntered(t *testing.T, p *sourcePlan) context.Context {
	t.Helper()
	select {
	case ctx := <-p.entered:
		return ctx
	case <-time.After(time.Second):
		t.Fatal("source RPC did not enter")
		return nil
	}
}

func TestSourceRPCDeadlinePolicy(t *testing.T) {
	f := &controlledSourceRPC{fakeMailboxRPC: &fakeMailboxRPC{}}
	conn, err := dialMailboxRPC(startMailboxRPC(t, f))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	client, err := NewMailboxClient(conn, 20*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	for _, short := range []bool{false, true} {
		p := newSourcePlan(t, []byte(`{}`))
		f.plans = append(f.plans, p)
		ctx, cancel := context.WithCancel(context.Background())
		if short {
			cancel()
			ctx, cancel = context.WithTimeout(context.Background(), 100*time.Millisecond)
		}
		done := make(chan *AppError, 1)
		go func() {
			_, ae := client.Call(ctx, liveBinding{}, "source_get", sourceRequest("source_get"))
			done <- ae
		}()
		serverCtx := sourceEntered(t, p)
		deadline, ok := serverCtx.Deadline()
		remaining := time.Until(deadline)
		if !ok || remaining > sourceRequestTimeout || !short && remaining < 29*time.Second || short && remaining > 100*time.Millisecond {
			t.Fatalf("incorrect source deadline: %v %s", ok, remaining)
		}
		if !short {
			cancel()
		}
		select {
		case ae := <-done:
			if ae == nil || ae.Code != "network.overloaded" {
				t.Fatalf("cancel/timeout became success: %v", ae)
			}
		case <-time.After(time.Second):
			t.Fatal("call stuck after cancellation")
		}
		cancel()
		close(p.release)
		<-p.returned
	}
	// Existing RPCs retain their configured short timeout.
	f.mu.Lock()
	f.block = true
	f.mu.Unlock()
	start := time.Now()
	_, ae := client.Call(context.Background(), liveBinding{}, "ready", nil)
	if ae == nil || ae.Code != "network.overloaded" || time.Since(start) > time.Second {
		t.Fatal("legacy timeout changed")
	}
}

func TestProductionSourceCancellationLateReplyDoesNotReleaseNewSlot(t *testing.T) {
	f := &controlledSourceRPC{fakeMailboxRPC: &fakeMailboxRPC{}}
	run, nc, down, b, cfg := sourceFixture(t, f.fakeMailboxRPC, f)
	response := sourceResult(t, cfg.ProtocolSigning, b, "source_get", "active")
	first, second := newSourcePlan(t, response), newSourcePlan(t, response)
	f.plans = []*sourcePlan{first, second}
	p := registrationPublicationCounter(run)
	lane := sourceLane{nodeIdentity{b.Tenant, b.Realm, b.Node}, false}
	publishRegistration(t, nc, b, sourceRequest("source_get"))
	sourceEntered(t, first)
	run.mailbox.mu.Lock()
	run.mailbox.sources[lane].cancel()
	run.mailbox.mu.Unlock()
	expectRegistrationCode(t, run, down, cfg.ProtocolSigning, b, "network.overloaded")
	run.mailbox.workers.Wait()
	if len(run.mailbox.slots) != 0 {
		t.Fatal("cancellation leaked regular slot")
	}
	publishRegistration(t, nc, b, sourceRequest("source_get"))
	sourceEntered(t, second)
	run.mailbox.mu.Lock()
	newCall := run.mailbox.sources[lane]
	run.mailbox.mu.Unlock()
	close(first.release)
	<-first.returned
	run.mailbox.mu.Lock()
	stillSame := run.mailbox.sources[lane] == newCall
	run.mailbox.mu.Unlock()
	if !stillSame || len(run.mailbox.slots) != 1 {
		t.Fatal("late reply released/replaced new slot")
	}
	p.mu.Lock()
	count := p.counts[DownSubject(b.Ingress)]
	p.mu.Unlock()
	if count != 1 {
		t.Fatalf("late canceled result was published: %d", count)
	}
	close(second.release)
	msg, err := down.NextMsg(time.Second)
	if err != nil || string(msg.Data) != string(response) {
		t.Fatalf("new call failed: %v %v", msg, err)
	}
	run.mailbox.workers.Wait()
}

func TestProductionSourceSuppressesOldConnectionResponses(t *testing.T) {
	for _, change := range []string{"tenant", "node", "epoch", "boot", "connection", "revoked", "forget"} {
		t.Run(change, func(t *testing.T) {
			f := &controlledSourceRPC{fakeMailboxRPC: &fakeMailboxRPC{}}
			run, nc, _, b, cfg := sourceFixture(t, f.fakeMailboxRPC, f)
			plan := newSourcePlan(t, sourceResult(t, cfg.ProtocolSigning, b, "source_get", "active"))
			f.plans = []*sourcePlan{plan}
			p := registrationPublicationCounter(run)
			publishRegistration(t, nc, b, sourceRequest("source_get"))
			sourceEntered(t, plan)
			if change == "forget" {
				run.redeemer.forget(b.Ingress)
			} else {
				guard := run.redeemer.connectionGuard(b.Ingress)
				guard.Lock()
				run.redeemer.mu.Lock()
				current := run.redeemer.bindings[b.Ingress]
				switch change {
				case "tenant":
					current.Tenant = "01900000-0000-7000-8000-000000000099"
				case "node":
					current.Node = "999"
				case "epoch":
					current.Epoch++
				case "boot":
					run.mailbox.sourceBoot[b.Ingress] = "next-boot"
				case "connection":
					current.ClientID++
				case "revoked":
					current.Revoked = true
				}
				run.redeemer.bindings[b.Ingress] = current
				run.redeemer.mu.Unlock()
				guard.Unlock()
			}
			close(plan.release)
			<-plan.returned
			run.mailbox.workers.Wait()
			p.mu.Lock()
			defer p.mu.Unlock()
			// A revoked connection may receive its signed node diagnostic only.
			want := 0
			if change == "revoked" || change == "boot" {
				want = 1
			}
			if p.counts[DownSubject(b.Ingress)] != want {
				t.Fatalf("wrong connection publication: %v", p.counts)
			}
			if change == "forget" && len(run.mailbox.sources) != 0 {
				t.Fatal("disconnect leaked lane")
			}
		})
	}
}

func TestProductionSourceRevokeLaneAndTenantHardBounds(t *testing.T) {
	f := &fakeMailboxRPC{}
	run, _, _, b, _ := sourceFixture(t, f, f)
	f.mu.Lock()
	f.block = true
	f.entered = make(chan struct{}, 200)
	f.mu.Unlock()
	// Hold the entire existing regular pool. Revocations have an independent
	// hard reservation, and one tenant can use at most two of those slots.
	for i := 0; i < cap(run.mailbox.slots); i++ {
		run.mailbox.slots <- struct{}{}
	}
	for i := 0; i < 3; i++ {
		q := b
		q.Ingress = ingressForTicket(fmt.Sprintf("same-tenant-%d", i))
		q.Node = fmt.Sprint(i + 2)
		q.CheckAfter = time.Now().Add(time.Hour)
		run.redeemer.mu.Lock()
		run.redeemer.bindings[q.Ingress] = q
		run.mailbox.sourceBoot[q.Ingress] = "boot-1"
		run.redeemer.mu.Unlock()
		run.mailbox.receive(&nats.Msg{Subject: UpSubject(q.Ingress), Data: sourceRequest("source_revoke")})
	}
	if len(run.mailbox.sourceRevokes) != 2 {
		t.Fatalf("tenant revoke bound/reservation: %d", len(run.mailbox.sourceRevokes))
	}
	for i := 0; i < 15; i++ {
		q := b
		q.Ingress = ingressForTicket(fmt.Sprintf("other-tenant-%d", i))
		q.Tenant = fmt.Sprintf("01900000-0000-7000-8000-%012d", i+10)
		q.CheckAfter = time.Now().Add(time.Hour)
		run.redeemer.mu.Lock()
		run.redeemer.bindings[q.Ingress] = q
		run.mailbox.sourceBoot[q.Ingress] = "boot-1"
		run.redeemer.mu.Unlock()
		run.mailbox.receive(&nats.Msg{Subject: UpSubject(q.Ingress), Data: sourceRequest("source_revoke")})
	}
	if len(run.mailbox.sourceRevokes) != 16 {
		t.Fatalf("global revoke bound=%d", len(run.mailbox.sourceRevokes))
	}
	for i := 0; i < 16; i++ {
		select {
		case <-f.entered:
		case <-time.After(time.Second):
			t.Fatal("reserved RPC did not enter")
		}
	}
	for i := 0; i < cap(run.mailbox.slots); i++ {
		<-run.mailbox.slots
	}
	run.Close()
	if len(run.mailbox.sourceRevokes) != 0 || len(run.mailbox.sources) != 0 || len(run.mailbox.errorSlots) != 0 {
		t.Fatal("shutdown leaked bounded pools")
	}
}

func TestProductionSourceWSSReconnectCancelsOldIngress(t *testing.T) {
	authority := &fakeIAM{active: true}
	f := &controlledSourceRPC{fakeMailboxRPC: &fakeMailboxRPC{}}
	run, nc, _, b, cfg := sourceFixture(t, f.fakeMailboxRPC, f, authority)
	first := newSourcePlan(t, sourceResult(t, cfg.ProtocolSigning, b, "source_get", "active"))
	f.plans = []*sourcePlan{first}
	p := registrationPublicationCounter(run)
	publishRegistration(t, nc, b, sourceRequest("source_get"))
	oldCtx := sourceEntered(t, first)
	url, tlsConfig := nc.ConnectedUrl(), nc.Opts.TLSConfig.Clone()
	nc.Close()
	select {
	case <-oldCtx.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("disconnect did not cancel old source RPC")
	}
	run.mailbox.workers.Wait()
	authority.mu.Lock()
	authority.used = false
	authority.mu.Unlock()
	ticket := strings.Replace(connectTicket, strings.Repeat("A", 43), strings.Repeat("B", 43), 1)
	fresh, err := nats.Connect(url, nats.Token(ticket), nats.NoReconnect(), nats.Secure(tlsConfig))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(fresh.Close)
	ingress := ingressForTicket(strings.Repeat("B", 43))
	run.redeemer.mu.Lock()
	next := run.redeemer.bindings[ingress]
	run.redeemer.mu.Unlock()
	next.Epoch, next.Incarnation, next.ChannelNonce = 43, "inc-1", "nonce-2"
	down, err := fresh.SubscribeSync(DownSubject(ingress))
	if err != nil {
		t.Fatal(err)
	}
	if err = fresh.Flush(); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.response = signedMailboxFrame(t, cfg.ProtocolSigning, "ready", next, "ready-2")
	f.mu.Unlock()
	next = sourceReady(t, run, fresh, down, next, cfg.ProtocolSigning, "ready-2", []byte(`{"app_version":"1.0","type":"ready","request_id":"ready-2","channel_nonce":"nonce-2","boot_id":"boot-2"}`))
	second := newSourcePlan(t, sourceResult(t, cfg.ProtocolSigning, next, "source_get", "active"))
	f.planMu.Lock()
	f.plans = append(f.plans, second)
	f.planMu.Unlock()
	raw := strings.Replace(string(sourceRequest("source_get")), `"epoch":"42"`, `"epoch":"43"`, 1)
	raw = strings.Replace(raw, `"boot-1"`, `"boot-2"`, 1)
	publishRegistration(t, fresh, next, []byte(raw))
	sourceEntered(t, second)
	close(first.release)
	<-first.returned
	lane := sourceLane{nodeIdentity{next.Tenant, next.Realm, next.Node}, false}
	run.mailbox.mu.Lock()
	call := run.mailbox.sources[lane]
	run.mailbox.mu.Unlock()
	if call == nil || call.ingress != next.Ingress {
		t.Fatal("old reply disturbed new ingress slot")
	}
	close(second.release)
	msg, err := down.NextMsg(time.Second)
	if err != nil || string(msg.Data) != string(second.response) {
		t.Fatalf("new ingress failed: %v %v", msg, err)
	}
	run.mailbox.workers.Wait()
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.counts[DownSubject(b.Ingress)] != 0 || p.counts[DownSubject(next.Ingress)] != 2 {
		t.Fatalf("cross-connection publication: %v", p.counts)
	}
}
