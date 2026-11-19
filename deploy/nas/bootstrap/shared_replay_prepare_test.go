package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"
	"github.com/nats-io/nkeys"
)

type replayPrepareStatus struct {
	jetstream.KeyValueStatus
	info *jetstream.StreamInfo
}

func (s replayPrepareStatus) StreamInfo() *jetstream.StreamInfo { return s.info }

type replayPrepareKV struct {
	jetstream.KeyValue
	info *jetstream.StreamInfo
}

func (k replayPrepareKV) Status(context.Context) (jetstream.KeyValueStatus, error) {
	return replayPrepareStatus{info: k.info}, nil
}

type replayPrepareFixture struct {
	initial    jetstream.KeyValue
	err        error
	created    jetstream.KeyValue
	config     jetstream.KeyValueConfig
	requests   []string
	createRuns int
}

func (f *replayPrepareFixture) KeyValue(_ context.Context, bucket string) (jetstream.KeyValue, error) {
	f.requests = append(f.requests, bucket)
	return f.initial, f.err
}
func (f *replayPrepareFixture) CreateKeyValue(_ context.Context, config jetstream.KeyValueConfig) (jetstream.KeyValue, error) {
	f.createRuns++
	f.config = config
	return f.created, nil
}

func replayPrepareNormalInfo() *jetstream.StreamInfo {
	// Pinned nats.go1.49.0 prepareKeyValueConfig's actual non-evicting defaults.
	return &jetstream.StreamInfo{Created: time.Date(2026, 11, 19, 0, 0, 0, 0, time.UTC), Config: jetstream.StreamConfig{Name: "KV_" + sharedReplayBucket, Subjects: []string{"$KV." + sharedReplayBucket + ".>"}, Storage: jetstream.FileStorage, Retention: jetstream.LimitsPolicy, Discard: jetstream.DiscardNew, MaxMsgs: -1, MaxBytes: -1, MaxMsgSize: -1, MaxConsumers: -1, MaxMsgsPerSubject: 1, Replicas: 1, MaxAge: 12 * time.Hour, AllowDirect: true, AllowRollup: true, DenyDelete: true, Duplicates: 2 * time.Minute}}
}

func TestSharedReplayPrepareCreatesOnlyTheMissingFixedBucketAndPreservesItsIdentity(t *testing.T) {
	ctx := context.Background()
	info := replayPrepareNormalInfo()
	missing := &replayPrepareFixture{err: jetstream.ErrBucketNotFound, created: replayPrepareKV{info: info}}
	created, err := prepareSharedReplayBucket(ctx, missing, time.Time{})
	if err != nil || !created.Equal(info.Created) || missing.createRuns != 1 || len(missing.requests) != 1 || missing.requests[0] != sharedReplayBucket || missing.config.Bucket != sharedReplayBucket || missing.config.TTL != 12*time.Hour || missing.config.Storage != jetstream.FileStorage || missing.config.History != 1 || missing.config.Replicas != 1 || missing.config.MaxBytes != -1 {
		t.Fatal("fixed first installation did not use the pinned KV producer", err)
	}
	for _, previous := range []time.Time{{}, info.Created} {
		existing := &replayPrepareFixture{initial: replayPrepareKV{info: info}}
		if _, err := prepareSharedReplayBucket(ctx, existing, previous); err != nil || existing.createRuns != 0 {
			t.Fatal("existing valid replay bucket was mutated", err)
		}
	}
	missing.createRuns = 0
	if _, err := prepareSharedReplayBucket(ctx, missing, info.Created); err == nil || missing.createRuns != 0 {
		t.Fatal("lost replay ledger was recreated")
	}
	for name, change := range map[string]func(*jetstream.StreamInfo){
		"sources": func(s *jetstream.StreamInfo) {
			s.Config.Sources = []*jetstream.StreamSource{{Name: "KV_auriga-sessions"}}
		},
		"discard-old":     func(s *jetstream.StreamInfo) { s.Config.Discard = jetstream.DiscardOld },
		"bounded-count":   func(s *jetstream.StreamInfo) { s.Config.MaxMsgs = 1 },
		"bounded-bytes":   func(s *jetstream.StreamInfo) { s.Config.MaxBytes = 1024 },
		"short-ttl":       func(s *jetstream.StreamInfo) { s.Config.MaxAge = time.Hour },
		"foreign-subject": func(s *jetstream.StreamInfo) { s.Config.Subjects = []string{"$KV.auriga-sessions.>"} },
		"new-ledger":      func(s *jetstream.StreamInfo) { s.Created = s.Created.Add(time.Minute) },
	} {
		t.Run(name, func(t *testing.T) {
			bad := replayPrepareNormalInfo()
			change(bad)
			fixture := &replayPrepareFixture{initial: replayPrepareKV{info: bad}}
			if _, err := prepareSharedReplayBucket(ctx, fixture, info.Created); err == nil || fixture.createRuns != 0 {
				t.Fatal("hostile existing stream was accepted or changed")
			}
		})
	}
}

func TestSharedReplayPrepareAuthorityExistsOnlyInTheSeparateConfiguration(t *testing.T) {
	edge, err := nkeys.CreateUser()
	if err != nil {
		t.Fatal(err)
	}
	defer edge.Wipe()
	installer, err := nkeys.CreateUser()
	if err != nil {
		t.Fatal(err)
	}
	defer installer.Wipe()
	edgePublic, _ := edge.PublicKey()
	installerPublic, _ := installer.PublicKey()
	original := []byte("users: [\n" + sharedNATSUserMarker + "\n{user: original, password: synthetic}\n]\npermissions: [" + sharedNATSPlatformMarker + "]")
	final, err := sharedNATSProjection(original, edgePublic)
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := sharedReplayPrepareNATS(original, edgePublic, installerPublic)
	if err != nil {
		t.Fatal(err)
	}
	user, _ := sharedReplayInstallerUser(installerPublic)
	if !bytes.Contains(prepared, []byte(user)) || !bytes.Equal(bytes.Replace(prepared, []byte(user+"\n"), nil, 1), final) || bytes.Contains(final, []byte(installerPublic)) || bytes.Contains(final, []byte("$JS.API.STREAM.CREATE.KV_auriga-shared-entry")) || bytes.Contains(final, []byte("$JS.API.STREAM.UPDATE.KV_auriga-shared-entry")) {
		t.Fatal("temporary installer authority leaked into resident configuration")
	}
	for _, forbidden := range []string{"$JS.API.>", "UPDATE", "DELETE", "MSG.GET", "$KV.", "CONSUMER", "_INBOX.>"} {
		if strings.Contains(user, forbidden) {
			t.Fatal("installer has foreign/read/update permissions")
		}
	}
	if _, err := sharedReplayPrepareNATS(original, edgePublic, edgePublic); err == nil {
		t.Fatal("shared installer and resident identity accepted")
	}
}
