package network

import (
	"errors"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
)

// Gate 5: the User JWT exp closes the connection; reconnecting with the
// same ticket is refused, so expiry is a hard backstop.

func TestGate5_JWTExpiryDisconnects(t *testing.T) {
	h := newHarness(t, defaultBrokerOpts())
	ad := h.startAdapter()
	// JWT exp has one-second resolution; aim ~2s ahead.
	notAfter := time.Now().Add(2 * time.Second).Truncate(time.Second).Add(time.Second)
	c := h.connectNew(notAfter)
	d := h.lastDecision()
	if d.uc.Expires != notAfter.Unix() {
		t.Fatalf("exp %d want %d", d.uc.Expires, notAfter.Unix())
	}
	if err := c.nc.Publish(UpSubject(c.ingress), []byte("before")); err != nil {
		t.Fatal(err)
	}
	ad.expectUp(t)
	waitClosed(t, c, 5*time.Second)
	closedAt := time.Now()
	t.Logf("closed %v after exp, last error: %v", closedAt.Sub(notAfter).Round(time.Millisecond), c.nc.LastError())
	if closedAt.Before(notAfter.Add(-time.Second)) {
		t.Fatalf("closed too early: %v before exp", notAfter.Sub(closedAt))
	}
	if err := c.nc.Publish(UpSubject(c.ingress), []byte("after")); !errors.Is(err, nats.ErrConnectionClosed) {
		t.Fatalf("publish after expiry: %v", err)
	}
	ad.expectNoUp(t, 300*time.Millisecond)
}

func TestGate5_AutoReconnectCannotReuseTicket(t *testing.T) {
	h := newHarness(t, defaultBrokerOpts())
	tk, ing := h.tickets.issue(t, time.Now().Add(2*time.Second).Truncate(time.Second).Add(time.Second))
	reconnects := make(chan struct{}, 8)
	c, err := h.connect(h.wsURL(), tk, ing,
		func(o *nats.Options) error { o.AllowReconnect = true; return nil }, // undo the harness NoReconnect
		nats.MaxReconnects(3), nats.ReconnectWait(100*time.Millisecond),
		nats.ReconnectHandler(func(*nats.Conn) { reconnects <- struct{}{} }))
	if err != nil {
		t.Fatal(err)
	}
	h.lastDecision()
	waitClosed(t, c, 8*time.Second)
	select {
	case <-reconnects:
		t.Fatal("SDK reconnected with a used ticket")
	default:
	}
	// Each reconnect attempt reached the callout and was refused as a replay.
	replays := 0
	for {
		select {
		case d := <-h.decisions:
			if !errors.Is(d.err, errTicket) {
				t.Fatalf("reconnect decision: %v", d.err)
			}
			replays++
			continue
		default:
		}
		break
	}
	if replays == 0 {
		t.Fatal("no reconnect attempt reached the callout")
	}
	t.Logf("%d reconnect attempts refused as ticket replay; last error: %v", replays, c.nc.LastError())
}
