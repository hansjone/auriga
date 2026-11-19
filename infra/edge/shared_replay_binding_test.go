package main

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

func sharedReplayNormalStream() jetstream.StreamConfig {
	return jetstream.StreamConfig{Name: "KV_" + sharedEntryBucket, Subjects: []string{"$KV." + sharedEntryBucket + ".>"}, Storage: jetstream.FileStorage, Retention: jetstream.LimitsPolicy, MaxMsgsPerSubject: 1, Replicas: 1, MaxAge: 12 * time.Hour, AllowDirect: true}
}

type sharedReplayFixtureStatus struct {
	jetstream.KeyValueStatus
	config jetstream.StreamConfig
}

func (s sharedReplayFixtureStatus) StreamInfo() *jetstream.StreamInfo {
	return &jetstream.StreamInfo{Config: s.config}
}
func (s sharedReplayFixtureStatus) TTL() time.Duration { return s.config.MaxAge }

type sharedReplayFixtureEntry struct {
	jetstream.KeyValueEntry
	raw []byte
}

func (e sharedReplayFixtureEntry) Value() []byte { return bytes.Clone(e.raw) }

type sharedReplayFixtureKV struct {
	jetstream.KeyValue
	config         jetstream.StreamConfig
	values         map[string][]byte
	creates, reads int
}

func (k *sharedReplayFixtureKV) Status(context.Context) (jetstream.KeyValueStatus, error) {
	return sharedReplayFixtureStatus{config: k.config}, nil
}
func (k *sharedReplayFixtureKV) Create(_ context.Context, key string, value []byte, _ ...jetstream.KVCreateOpt) (uint64, error) {
	k.creates++
	if _, ok := k.values[key]; ok {
		return 0, jetstream.ErrKeyExists
	}
	k.values[key] = bytes.Clone(value)
	return 1, nil
}
func (k *sharedReplayFixtureKV) Get(_ context.Context, key string) (jetstream.KeyValueEntry, error) {
	k.reads++
	raw, ok := k.values[key]
	if !ok {
		return nil, jetstream.ErrKeyNotFound
	}
	return sharedReplayFixtureEntry{raw: raw}, nil
}

type sharedReplayFixtureReader struct {
	kv        jetstream.KeyValue
	err       error
	requested []string
}

func (r *sharedReplayFixtureReader) KeyValue(_ context.Context, bucket string) (jetstream.KeyValue, error) {
	r.requested = append(r.requested, bucket)
	return r.kv, r.err
}

func TestSharedReplayBindingRejectsSameNameSourcesPayloadAndForeignStreamShape(t *testing.T) {
	valid := sharedReplayNormalStream()
	for _, ttl := range []time.Duration{0, 12 * time.Hour, 24 * time.Hour} {
		candidate := valid
		candidate.MaxAge = ttl
		if err := validateSharedReplayStream(candidate); err != nil {
			t.Fatal("normal preinstalled retention rejected", err)
		}
	}
	for name, alter := range map[string]func(*jetstream.StreamConfig){
		"sources":         func(c *jetstream.StreamConfig) { c.Sources = []*jetstream.StreamSource{{Name: "KV_auriga-sessions"}} },
		"mirror":          func(c *jetstream.StreamConfig) { c.Mirror = &jetstream.StreamSource{Name: "KV_auriga-sessions"} },
		"foreign-subject": func(c *jetstream.StreamConfig) { c.Subjects = []string{"$KV.auriga-sessions.>"} },
		"additional-subject": func(c *jetstream.StreamConfig) {
			c.Subjects = append(append([]string{}, c.Subjects...), "$KV.auriga-sessions.>")
		},
		"transform": func(c *jetstream.StreamConfig) {
			c.SubjectTransform = &jetstream.SubjectTransformConfig{Source: ">", Destination: "$KV.auriga-sessions.>"}
		},
		"republish":       func(c *jetstream.StreamConfig) { c.RePublish = &jetstream.RePublish{Destination: "foreign"} },
		"short-ttl":       func(c *jetstream.StreamConfig) { c.MaxAge = time.Hour },
		"per-message-ttl": func(c *jetstream.StreamConfig) { c.AllowMsgTTL = true },
		"memory":          func(c *jetstream.StreamConfig) { c.Storage = jetstream.MemoryStorage },
		"wrong-name":      func(c *jetstream.StreamConfig) { c.Name = "KV_auriga-sessions" },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := valid
			alter(&candidate)
			kv := &sharedReplayFixtureKV{config: candidate}
			reader := &sharedReplayFixtureReader{kv: kv}
			if _, err := bindSharedReplayBucket(context.Background(), reader); err == nil {
				t.Fatal("same-name hostile bucket accepted")
			}
		})
	}
	// Reproduce the reported raw CREATE/UPDATE JSON payload, rather than only
	// looking for a foreign name in an authorization subject string.
	payload := []byte(`{"name":"KV_auriga-shared-entry","subjects":["$KV.auriga-shared-entry.>"],"storage":"file","retention":"limits","max_msgs_per_subject":1,"num_replicas":1,"max_age":43200000000000,"sources":[{"name":"KV_auriga-sessions"}]}`)
	var imported jetstream.StreamConfig
	if err := json.Unmarshal(payload, &imported); err != nil {
		t.Fatal(err)
	}
	if err := validateSharedReplayStream(imported); err == nil {
		t.Fatal("Sources payload bypassed dedicated bucket boundary")
	}
	missing := &sharedReplayFixtureReader{err: jetstream.ErrBucketNotFound}
	if _, err := bindSharedReplayBucket(context.Background(), missing); err == nil || !strings.Contains(err.Error(), "shared-replay-prepare") {
		t.Fatal("missing bucket did not require public installation", err)
	}
}

func TestSharedReplayPreinstalledBucketKeepsAtomicCreateAndLookup(t *testing.T) {
	kv := &sharedReplayFixtureKV{config: sharedReplayNormalStream(), values: map[string][]byte{}}
	reader := &sharedReplayFixtureReader{kv: kv}
	bound, err := bindSharedReplayBucket(context.Background(), reader)
	if err != nil {
		t.Fatal(err)
	}
	if len(reader.requested) != 1 || reader.requested[0] != sharedEntryBucket {
		t.Fatal("binding requested another bucket")
	}
	store := &NATSSharedEntryStore{KV: bound}
	jti := "11111111-2222-4333-8444-555555555555"
	cookie := jti + ".synthetic-cookie"
	binding := SharedBrowserBinding{Binding: map[string]any{"memberId": "synthetic-member"}, PlatformSessionID: "synthetic-session", ExpiresAt: time.Now().Add(time.Hour)}
	if err := store.ConsumeAndBind(context.Background(), jti, time.Now().Add(time.Minute), cookie, binding); err != nil {
		t.Fatal(err)
	}
	if err := store.ConsumeAndBind(context.Background(), jti, time.Now().Add(time.Minute), cookie, binding); err == nil || err.Error() != "common.conflict" {
		t.Fatal("ticket CAS replay rejection lost", err)
	}
	got, err := store.Lookup(context.Background(), cookie)
	if err != nil || got.PlatformSessionID != binding.PlatformSessionID || got.Binding["memberId"] != binding.Binding["memberId"] {
		t.Fatal("preinstalled ticket binding was not readable", err)
	}
	if _, err := store.Lookup(context.Background(), jti+".forged"); err == nil {
		t.Fatal("forged cookie accepted")
	}
	if kv.creates != 2 || kv.reads != 2 {
		t.Fatal("normal path did not use only ticket Create/Get")
	}
}
