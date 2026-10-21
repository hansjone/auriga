package nastest

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	natsserver "github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// Run from infra/edge: go test -count=1 ../../deploy/nas/test/nats_policy_test.go.
// Reuse the repository's existing NATS versions without adding production dependencies.
func TestNASNATSPolicy(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	raw, err := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "nats", "nats.conf.tmpl"))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	text := string(raw)
	for _, name := range []string{"SYS", "PLATFORM", "ROUTES_RW", "EDGE"} {
		text = strings.ReplaceAll(text, "__NATS_"+name+"_PASSWORD__", "test-only-password")
	}
	text = strings.Replace(text, "store_dir: /data", "store_dir: "+strconv.Quote(filepath.Join(dir, "js")), 1)
	config := filepath.Join(dir, "nats.conf")
	if err := os.WriteFile(config, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	opts, err := natsserver.ProcessConfigFile(config)
	if err != nil {
		t.Fatal(err)
	}
	opts.Host, opts.Port, opts.HTTPPort = "127.0.0.1", -1, 0
	opts.NoLog, opts.NoSigs = true, true
	srv, err := natsserver.NewServer(opts)
	if err != nil {
		t.Fatal(err)
	}
	go srv.Start()
	t.Cleanup(func() { srv.Shutdown(); srv.WaitForShutdown() })
	if !srv.ReadyForConnections(5 * time.Second) {
		t.Fatal("NATS not ready")
	}
	connect := func(user string) (*nats.Conn, jetstream.JetStream, <-chan error) {
		t.Helper()
		errors := make(chan error, 16)
		nc, err := nats.Connect(srv.ClientURL(), nats.UserInfo(user, "test-only-password"),
			nats.NoReconnect(), nats.Timeout(time.Second),
			nats.ErrorHandler(func(_ *nats.Conn, _ *nats.Subscription, err error) { errors <- err }))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(nc.Close)
		js, err := jetstream.New(nc)
		if err != nil {
			t.Fatal(err)
		}
		return nc, js, errors
	}
	platformConn, platform, _ := connect("platform")
	_, routes, _ := connect("routes_rw")
	edgeConn, edge, edgeErrors := connect("edge")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	for name, subject := range map[string]string{"AURIGA_EVENTS": "auriga.evt.>", "AURIGA_AUDIT": "auriga.audit.>"} {
		if _, err := platform.CreateStream(ctx, jetstream.StreamConfig{Name: name, Subjects: []string{subject}}); err != nil {
			t.Fatalf("platform first-boot stream %s: %v", name, err)
		}
	}
	// Java ensureStreams lists names on both first boot and restart.
	for range 2 {
		names := platform.StreamNames(ctx)
		count := 0
		for range names.Name() {
			count++
		}
		if names.Err() != nil || count != 2 {
			t.Fatalf("list streams: count=%d error=%v", count, names.Err())
		}
	}
	sessions, err := platform.CreateKeyValue(ctx, jetstream.KeyValueConfig{Bucket: "auriga-sessions"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sessions.Put(ctx, "cookiehash", []byte("session")); err != nil {
		t.Fatal(err)
	}
	edgeSessions, err := edge.KeyValue(ctx, "auriga-sessions")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := edgeSessions.Get(ctx, "cookiehash"); err != nil {
		t.Fatalf("edge session read: %v", err)
	}
	routeKV, err := routes.CreateKeyValue(ctx, jetstream.KeyValueConfig{Bucket: "auriga-routes"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := routeKV.Put(ctx, "instance1", []byte("route")); err != nil {
		t.Fatal(err)
	}
	edgeRoutes, err := edge.KeyValue(ctx, "auriga-routes")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := edgeRoutes.Get(ctx, "instance1"); err != nil {
		t.Fatalf("edge route read: %v", err)
	}
	for _, stream := range []string{"AURIGA_EVENTS", "AURIGA_AUDIT"} {
		consumer, err := platform.CreateOrUpdateConsumer(ctx, stream, jetstream.ConsumerConfig{
			Durable: "platform-test", AckPolicy: jetstream.AckExplicitPolicy,
		})
		if err != nil {
			t.Fatalf("platform durable %s: %v", stream, err)
		}
		subject := "auriga.audit.platform"
		if stream == "AURIGA_EVENTS" {
			subject = "auriga.evt.identity.session_revoked"
		}
		if _, err := platform.Publish(ctx, subject, []byte("event")); err != nil {
			t.Fatal(err)
		}
		msg, err := consumer.Next(jetstream.FetchMaxWait(time.Second))
		if err != nil {
			t.Fatalf("platform pull %s: %v", stream, err)
		}
		if err := msg.DoubleAck(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := edge.Stream(ctx, "AURIGA_EVENTS"); err != nil {
		t.Fatal(err)
	}
	consumer, err := edge.CreateOrUpdateConsumer(ctx, "AURIGA_EVENTS", jetstream.ConsumerConfig{
		Durable: "edge-session-revoke", FilterSubject: "auriga.evt.identity.session_revoked",
		AckPolicy: jetstream.AckExplicitPolicy, DeliverPolicy: jetstream.DeliverNewPolicy,
	})
	if err != nil {
		t.Fatalf("edge revoke durable: %v", err)
	}
	if _, err := platform.Publish(ctx, "auriga.evt.identity.session_revoked", []byte("revoke")); err != nil {
		t.Fatal(err)
	}
	msg, err := consumer.Next(jetstream.FetchMaxWait(time.Second))
	if err != nil || string(msg.Data()) != "revoke" {
		t.Fatalf("edge revoke pull: %v", err)
	}
	if err := msg.DoubleAck(ctx); err != nil {
		t.Fatal(err)
	}
	if err := edgeSessions.Delete(ctx, "cookiehash"); err != nil {
		t.Fatalf("edge session deletion: %v", err)
	}
	// Request timeouts alone are insufficient: require the server's explicit ACL rejection.
	deny := func(subject string) {
		t.Helper()
		if _, err := edgeConn.Request(subject, []byte("{}"), 300*time.Millisecond); err == nil {
			t.Fatalf("unexpected permission for %s", subject)
		}
		select {
		case err := <-edgeErrors:
			if !strings.Contains(strings.ToLower(err.Error()), "permissions violation") || !strings.Contains(err.Error(), subject) {
				t.Fatalf("expected ACL violation for %s: %v", subject, err)
			}
		case <-time.After(time.Second):
			t.Fatalf("no server ACL rejection for %s", subject)
		}
	}
	deny("$JS.API.STREAM.DELETE.AURIGA_EVENTS")
	deny("$JS.API.STREAM.CREATE.UNRELATED")
	deny("$JS.API.CONSUMER.CREATE.AURIGA_EVENTS.foreign")
	deny("$KV.auriga-routes.instance1")
	if err := platformConn.FlushTimeout(time.Second); err != nil {
		t.Fatal(err)
	}
}
