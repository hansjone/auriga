package network

import (
	"strings"
	"testing"
	"time"
)

// Gate 2: negative ACL matrix with two real DSH-like clients A and B.

func TestGate2_NegativeACLMatrix(t *testing.T) {
	h := newHarness(t, defaultBrokerOpts())
	ad := h.startAdapter()
	exp := time.Now().Add(time.Hour)
	A := h.connectNew(exp)
	B := h.connectNew(exp)
	// B listens on its own down subject; A must never see B's traffic.
	bDown, err := B.nc.SubscribeSync(DownSubject(B.ingress))
	if err != nil {
		t.Fatal(err)
	}
	_ = B.nc.Flush()
	B.noAsyncErr(t)

	denySubs := []string{
		DownSubject(B.ingress),
		"auriga.net.down.*",
		"auriga.net.down.>",
		"auriga.net.>",
		">",
		"*",
		"$SYS.>",
		"$SYS.REQ.SERVER.PING",
		"$JS.API.>",
		"$JS.API.STREAM.LIST",
		"$KV.>",
		"auriga.internal.>",
		UpSubject(A.ingress),
		UpSubject(B.ingress),
		"auriga.net.up.*",
		"_INBOX.>",
		"_INBOX.abc.*",
		ReplyPrefix(B.ingress) + ".>",
		"auriga.net.reply.>",
		"auriga.net.reply.*.>",
		ReplyPrefix(A.ingress), // the bare prefix is outside "<prefix>.>"
	}
	for _, subj := range denySubs {
		if _, err := A.nc.SubscribeSync(subj); err != nil {
			t.Fatalf("local subscribe %q: %v", subj, err)
		}
		_ = A.nc.Flush()
		A.expectAsyncErr(t, `subscription to "`+subj+`"`)
	}
	// Queue subscriptions are checked the same way.
	if _, err := A.nc.QueueSubscribeSync(DownSubject(B.ingress), "q"); err != nil {
		t.Fatal(err)
	}
	_ = A.nc.Flush()
	A.expectAsyncErr(t, `subscription to "`+DownSubject(B.ingress))

	denyPubs := []string{
		UpSubject(B.ingress),
		DownSubject(A.ingress),
		DownSubject(B.ingress),
		"auriga.net.up.x",
		"$SYS.REQ.SERVER.PING",
		"$SYS.REQ.SERVER." + h.srv.ID() + ".KICK",
		AuthCalloutSubject,
		"$JS.API.INFO",
		"$KV.bucket.key",
		"auriga.internal.cmd",
		"_INBOX.someone.abc",
		ReplyPrefix(B.ingress) + ".nuid.tok",
		ReplyPrefix(A.ingress) + ".nuid.tok",
	}
	for _, subj := range denyPubs {
		if err := A.nc.Publish(subj, []byte("x")); err != nil {
			t.Fatalf("local publish %q: %v", subj, err)
		}
		_ = A.nc.Flush()
		A.expectAsyncErr(t, `publish to "`+subj+`"`)
	}
	ad.expectNoUp(t, 300*time.Millisecond)
	if m, err := bDown.NextMsg(200 * time.Millisecond); err == nil {
		t.Fatalf("B received forged traffic: %q on %s", m.Data, m.Subject)
	}

	// Positive control: adapter -> B down reaches B only.
	if err := ad.nc.Publish(DownSubject(B.ingress), []byte("for-b")); err != nil {
		t.Fatal(err)
	}
	if m, err := bDown.NextMsg(2 * time.Second); err != nil || string(m.Data) != "for-b" {
		t.Fatalf("B down: %v", err)
	}
	// Internal service subjects never cross into the client account.
	if err := ad.nc.Publish("auriga.internal.secret", []byte("s")); err != nil {
		t.Fatal(err)
	}
	_ = ad.nc.Flush()
	B.noAsyncErr(t)
}

func TestGate2_ForgedIdentityAndArbitraryReply(t *testing.T) {
	h := newHarness(t, defaultBrokerOpts())
	ad := h.startAdapter()
	exp := time.Now().Add(time.Hour)
	A := h.connectNew(exp)
	B := h.connectNew(exp)
	bDown, _ := B.nc.SubscribeSync(DownSubject(B.ingress))
	bReply, _ := B.nc.SubscribeSync(ReplyPrefix(B.ingress) + ".>")
	_ = B.nc.Flush()
	B.noAsyncErr(t)

	// Payload claims to be B; the adapter only trusts the up subject.
	forged := `{"tenant":"t-other","node":"node-b","ingress":"` + B.ingress + `"}`
	if err := A.nc.Publish(UpSubject(A.ingress), []byte(forged)); err != nil {
		t.Fatal(err)
	}
	if u := ad.expectUp(t); u.ingress != A.ingress {
		t.Fatalf("adapter attributed message to %q, want %q", u.ingress, A.ingress)
	}

	// The broker does not police the reply field of PUB: A can name any
	// subject as reply. The adapter must refuse to answer outside A's prefix.
	for _, reply := range []string{
		DownSubject(B.ingress),
		ReplyPrefix(B.ingress) + ".nuid.tok",
		"_INBOX.victim.x",
		"$SYS.REQ.SERVER.PING",
		ReplyPrefix(A.ingress) + ".nuid.*",
		ReplyPrefix(A.ingress) + ".a.b.c",
		ReplyPrefix(A.ingress),
	} {
		if err := A.nc.PublishRequest(UpSubject(A.ingress), reply, []byte("req")); err != nil {
			t.Fatal(err)
		}
		_ = A.nc.Flush()
		A.noAsyncErr(t) // accepted by the broker ...
		u := ad.expectUp(t)
		if u.reply != reply {
			t.Fatalf("reply rewritten across stream import: %q -> %q", reply, u.reply)
		}
		select { // ... and refused by the adapter
		case r := <-ad.rejected:
			if r.reply != reply {
				t.Fatalf("rejected %q, want %q", r.reply, reply)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("adapter answered arbitrary reply %q", reply)
		}
	}
	if m, err := bDown.NextMsg(300 * time.Millisecond); err == nil {
		t.Fatalf("B got %q", m.Data)
	}
	if m, err := bReply.NextMsg(100 * time.Millisecond); err == nil {
		t.Fatalf("B reply got %q", m.Data)
	}
	t.Log("broker accepts any reply subject on PUB; adapter ValidReplyFor is the enforcement point")
}

func TestSubjectHelpers(t *testing.T) {
	ing, err := NewIngressID()
	if err != nil || !ValidIngressID(ing) || len(ing) != 26 {
		t.Fatalf("ingress %q %v", ing, err)
	}
	for _, bad := range []string{"", "*", ">", ing + ".x", strings.ToUpper(ing), ing[:25] + "1", ing[:25] + "."} {
		if ValidIngressID(bad) {
			t.Fatalf("accepted ingress %q", bad)
		}
		if _, err := IngressFromUp(upRoot + bad); err == nil {
			t.Fatalf("IngressFromUp accepted %q", bad)
		}
	}
	if got, err := IngressFromUp(UpSubject(ing)); err != nil || got != ing {
		t.Fatal(got, err)
	}
	if _, err := IngressFromUp(DownSubject(ing)); err == nil {
		t.Fatal("down accepted as up")
	}
	p := ReplyPrefix(ing)
	for reply, ok := range map[string]bool{
		p + ".AbC123.xYz9": true, // mux
		p + ".AbC123":      true, // noMux
		p:                  false,
		p + ".":            false,
		p + "..x":          false,
		p + ".a.b.c":       false,
		p + ".a.*":         false,
		p + ".>":           false,
		p + ".a-b":         false,
		p + "x.a":          false,
		"_INBOX.a.b":       false,
		"":                 false,
	} {
		if ValidReplyFor(ing, reply) != ok {
			t.Fatalf("ValidReplyFor(%q) != %v", reply, ok)
		}
	}
}
