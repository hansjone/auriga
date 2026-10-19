package main

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	natsserver "github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

func startTestNATS(t *testing.T, user, password string) (url string, cleanup func()) {
	t.Helper()
	dir := t.TempDir()
	opts := &natsserver.Options{
		Host: "127.0.0.1", Port: -1, JetStream: true, StoreDir: filepath.Join(dir, "js"),
		Username: user, Password: password, NoLog: true, NoSigs: true,
	}
	srv, err := natsserver.NewServer(opts)
	if err != nil {
		t.Fatal(err)
	}
	go srv.Start()
	if !srv.ReadyForConnections(5 * time.Second) {
		t.Fatal("nats not ready")
	}
	u := "nats://" + user + ":" + password + "@" + srv.Addr().String()
	return u, func() {
		srv.Shutdown()
		srv.WaitForShutdown()
	}
}

func TestB9_NATSRevokeDeletesSessionAndCancelsConn(t *testing.T) {
	url, stop := startTestNATS(t, "auriga", "test-pass")
	defer stop()

	nc, err := nats.Connect(url)
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()
	js, err := jetstream.New(nc)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	_, err = js.CreateStream(ctx, jetstream.StreamConfig{
		Name: "AURIGA_EVENTS", Subjects: []string{"auriga.evt.>"},
	})
	if err != nil {
		t.Fatal(err)
	}
	kv, err := js.CreateKeyValue(ctx, jetstream.KeyValueConfig{Bucket: sessionsKVBucket})
	if err != nil {
		t.Fatal(err)
	}

	hub := NewSessionConnHub()
	gotRevoke := make(chan string, 1)

	store, err := NewNATSSessionStore(url, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	store.SetOnRevoke(func(id string) {
		select {
		case gotRevoke <- id:
		default:
		}
		hub.CancelSession(id)
	})
	if err := store.WaitRevokeConsumer(10 * time.Second); err != nil {
		t.Fatal(err)
	}
	// Allow Consume interest to register with the server.
	time.Sleep(200 * time.Millisecond)

	sessionID := "22222222-2222-2222-2222-222222222222"
	cookieHash := "abcd"
	sess := &Session{
		SessionID: sessionID, AccountID: "a", TenantID: "t", Audience: "portal",
		ExpiresAt: time.Now().Add(time.Hour),
	}
	raw, _ := json.Marshal(sess)
	if _, err := kv.Put(ctx, cookieHash, raw); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get(ctx, cookieHash); err != nil {
		t.Fatalf("precondition get: %v", err)
	}

	connCtx, stopAttach := hub.Attach(context.Background(), sessionID, time.Time{})
	defer stopAttach()

	env := []byte(`{"type":"identity.session_revoked","data":{"accountId":"a","sessionIds":["` + sessionID + `"],"reason":"logout","actorId":null}}`)
	deadline := time.Now().Add(8 * time.Second)
	for {
		if _, err := js.Publish(ctx, sessionRevokeSubj, env); err != nil {
			t.Fatal(err)
		}
		select {
		case id := <-gotRevoke:
			if id != sessionID {
				t.Fatalf("revoked id %q", id)
			}
			goto revoked
		case <-time.After(300 * time.Millisecond):
			if time.Now().After(deadline) {
				t.Fatal("revoke event not delivered to edge consumer")
			}
		}
	}
revoked:

	select {
	case <-connCtx.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("expected long-lived connection canceled after revoke event")
	}

	if err := kv.Delete(ctx, cookieHash); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get(ctx, cookieHash); err == nil {
		t.Fatal("expected session gone after KV delete")
	}
}
