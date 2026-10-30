package network

import (
	"errors"
	"net"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
)

// Gate 4: the callout's (server_id, CID) lets a system-account identity kick
// exactly that connection; LDM is only a hint; restarts cannot cause a misfire.

func TestGate4_KickByCalloutBinding(t *testing.T) {
	h := newHarness(t, defaultBrokerOpts())
	ops := h.opsConn()
	ad := h.startAdapter()
	exp := time.Now().Add(time.Hour)
	victim := h.connectNew(exp)
	bystander := h.connectNew(exp)
	b := h.tickets.bindingFor(t, victim.ingress)
	if b.ServerID != h.srv.ID() {
		t.Fatalf("binding server %q != %q", b.ServerID, h.srv.ID())
	}
	if cid, _ := victim.nc.GetClientID(); cid != b.ClientID {
		t.Fatalf("callout CID %d != connection CID %d", b.ClientID, cid)
	}

	// A binding whose ingress does not match the live connection is refused.
	wrong := b
	wrong.Ingress = bystander.ingress
	if err := KickBinding(ctxT(t), ops, wrong); !errors.Is(err, ErrNotOwned) {
		t.Fatalf("mismatched binding: %v", err)
	}
	if !victim.nc.IsConnected() {
		t.Fatal("victim kicked by mismatched binding")
	}

	start := time.Now()
	if err := KickBinding(ctxT(t), ops, b); err != nil {
		t.Fatal(err)
	}
	took := waitClosed(t, victim, 3*time.Second)
	t.Logf("kick -> closed in %v (request %v), last error: %v", took, time.Since(start).Round(time.Millisecond), victim.nc.LastError())
	if err := KickBinding(ctxT(t), ops, b); !errors.Is(err, ErrNoSuchConn) {
		t.Fatalf("second kick: %v", err)
	}
	// Bystander untouched.
	if err := bystander.nc.Publish(UpSubject(bystander.ingress), []byte("alive")); err != nil {
		t.Fatal(err)
	}
	if u := ad.expectUp(t); u.ingress != bystander.ingress {
		t.Fatalf("got %+v", u)
	}

	// Ops identity is least-privilege: only KICK/LDM/CONNZ.
	opsErrs := make(chan error, 8)
	opsNC, err := nats.Connect(h.tcpURL(), nats.UserInfo(h.opsUser, h.opsPass), nats.NoReconnect(),
		nats.ErrorHandler(func(_ *nats.Conn, _ *nats.Subscription, err error) { opsErrs <- err }))
	if err != nil {
		t.Fatal(err)
	}
	defer opsNC.Close()
	for _, subj := range []string{"$SYS.REQ.SERVER." + h.srv.ID() + ".RELOAD", "$SYS.REQ.SERVER.PING", "$SYS.REQ.ACCOUNT.CLIENTS.CONNZ", DownSubject(bystander.ingress)} {
		_ = opsNC.Publish(subj, []byte("{}"))
		_ = opsNC.Flush()
		select {
		case err := <-opsErrs:
			t.Logf("ops denied: %v", err)
		case <-time.After(2 * time.Second):
			t.Fatalf("ops publish to %q not denied", subj)
		}
	}
}

func TestGate4_LameDuckIsHintOnly(t *testing.T) {
	h := newHarness(t, defaultBrokerOpts())
	ops := h.opsConn()
	ad := h.startAdapter()
	c := h.connectNew(time.Now().Add(time.Hour))
	b := h.tickets.bindingFor(t, c.ingress)
	if err := LameDuck(ctxT(t), ops, b.ServerID, b.ClientID); err != nil {
		t.Fatal(err)
	}
	select {
	case <-c.ldm:
	case <-time.After(3 * time.Second):
		t.Fatal("client did not see lame duck INFO")
	}
	time.Sleep(300 * time.Millisecond)
	if !c.nc.IsConnected() {
		t.Fatal("LDM closed the connection")
	}
	// Still fully authorized: LDM revokes nothing.
	if err := c.nc.Publish(UpSubject(c.ingress), []byte("after-ldm")); err != nil {
		t.Fatal(err)
	}
	ad.expectUp(t)
	if err := LameDuck(ctxT(t), ops, b.ServerID, b.ClientID+1000); err == nil {
		t.Fatal("LDM to unknown CID succeeded")
	}
}

func TestGate4_StaleKickAfterReconnectMissesNewConnection(t *testing.T) {
	h := newHarness(t, defaultBrokerOpts())
	ops := h.opsConn()
	exp := time.Now().Add(time.Hour)
	c1 := h.connectNew(exp)
	b1 := h.tickets.bindingFor(t, c1.ingress)
	c1.nc.Close()
	waitConnGone(t, h, ops, b1.ClientID)
	c2 := h.connectNew(exp)
	b2 := h.tickets.bindingFor(t, c2.ingress)
	if b2.ClientID <= b1.ClientID {
		t.Fatalf("CID not monotonic within one server: %d then %d", b1.ClientID, b2.ClientID)
	}
	// A delayed KICK for the old binding hits nothing.
	if err := KickBinding(ctxT(t), ops, b1); !errors.Is(err, ErrNoSuchConn) {
		t.Fatalf("stale KickBinding: %v", err)
	}
	if err := Kick(ctxT(t), ops, b1.ServerID, b1.ClientID); err == nil {
		t.Fatal("raw stale kick reported success")
	} else {
		t.Logf("raw stale kick: %v", err)
	}
	time.Sleep(200 * time.Millisecond)
	if !c2.nc.IsConnected() {
		t.Fatal("new connection hit by stale kick")
	}
}

func portOf(t *testing.T, rawURL string) int {
	t.Helper()
	u, err := url.Parse(rawURL)
	if err != nil {
		t.Fatal(err)
	}
	_, p, err := net.SplitHostPort(u.Host)
	if err != nil {
		t.Fatal(err)
	}
	n, _ := strconv.Atoi(p)
	return n
}

// restart stops the server and starts it again on the same ports with the
// same identities and keys, like a process restart.
func (h *harness) restart() {
	h.t.Helper()
	h.o.port, h.o.wsPort = portOf(h.t, h.tcpURL()), portOf(h.t, h.wsURL())
	h.stopServer()
	h.startServer()
	h.startCallout()
}

func TestGate4_RestartChangesServerIDSoOldKickCannotMisfire(t *testing.T) {
	h := newHarness(t, defaultBrokerOpts())
	exp := time.Now().Add(time.Hour)
	h.opsConn() // same connection order before and after restart -> same CIDs
	c1 := h.connectNew(exp)
	b1 := h.tickets.bindingFor(t, c1.ingress)
	oldID := h.srv.ID()

	h.restart()
	waitClosed(t, c1, 3*time.Second)
	if h.srv.ID() == oldID {
		t.Fatal("server_id survived restart; (server_id, cid) would not be restart-safe")
	}
	ops := h.opsConn()
	// Open connections until one reuses the old CID number on the new server.
	var c2 *client
	var b2 Binding
	for i := 0; i < 20; i++ {
		c := h.connectNew(exp)
		b := h.tickets.bindingFor(t, c.ingress)
		if b.ClientID == b1.ClientID {
			c2, b2 = c, b
			break
		}
		if b.ClientID > b1.ClientID {
			break
		}
	}
	if c2 == nil {
		t.Fatalf("could not reproduce CID reuse (old cid %d)", b1.ClientID)
	}
	t.Logf("old server %s cid %d; new server %s reuses cid %d", oldID, b1.ClientID, b2.ServerID, b2.ClientID)

	// The old kick is addressed to the old server_id: nobody answers.
	err := Kick(ctxT(t), ops, b1.ServerID, b1.ClientID)
	if !errors.Is(err, nats.ErrNoResponders) {
		t.Fatalf("old-server kick err = %v, want no responders", err)
	}
	if err := KickBinding(ctxT(t), ops, b1); err == nil {
		t.Fatal("KickBinding on old binding succeeded")
	}
	time.Sleep(200 * time.Millisecond)
	if !c2.nc.IsConnected() {
		t.Fatal("connection reusing the old CID was kicked")
	}
	// Counter-example: a kick keyed only by CID (ignoring server_id) would hit it.
	if err := Kick(ctxT(t), ops, h.srv.ID(), b1.ClientID); err != nil {
		t.Fatal(err)
	}
	waitClosed(t, c2, 3*time.Second)
	t.Log("CID-only addressing would have kicked the wrong connection; server_id scoping prevents it")
}
