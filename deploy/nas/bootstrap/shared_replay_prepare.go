package main

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/nats-io/nats.go/jetstream"
	"github.com/nats-io/nkeys"
)

const sharedReplayBucket = "auriga-shared-entry"

// No update/delete operation is exposed, including for an old same-name stream.
type sharedReplayInstaller interface {
	KeyValue(context.Context, string) (jetstream.KeyValue, error)
	CreateKeyValue(context.Context, jetstream.KeyValueConfig) (jetstream.KeyValue, error)
}

func sharedReplayInstallerUser(public string) (string, error) {
	if !nkeys.IsValidPublicUserKey(public) {
		return "", fmt.Errorf("dedicated installer public NKey required")
	}
	return fmt.Sprintf(`{
  nkey: %s
  permissions: {
    publish: { allow: ["$JS.API.INFO", "$JS.API.STREAM.INFO.KV_auriga-shared-entry", "$JS.API.STREAM.CREATE.KV_auriga-shared-entry"] }
    subscribe: { allow: ["_INBOX_auriga_shared_install.>"] }
  }
},`, strconv.Quote(public)), nil
}

func sharedReplayPrepareNATS(original []byte, edge, installer string) ([]byte, error) {
	if edge == installer {
		return nil, fmt.Errorf("resident and installer NKeys must differ")
	}
	user, err := sharedReplayInstallerUser(installer)
	if err != nil {
		return nil, err
	}
	// Reuse the exact permanent projection and its anchor validation. Installer
	// authority exists in this separate prepare config only, never in final.
	final, err := sharedNATSProjection(original, edge)
	if err != nil {
		return nil, err
	}
	edgeUser, err := sharedReplayNATSUser(edge)
	if err != nil {
		return nil, err
	}
	return []byte(strings.Replace(string(final), edgeUser, user+"\n"+edgeUser, 1)), nil
}

func sharedReplayStreamInfo(ctx context.Context, kv jetstream.KeyValue) (*jetstream.StreamInfo, error) {
	status, err := kv.Status(ctx)
	if err != nil || status == nil {
		return nil, fmt.Errorf("complete installed replay stream status unavailable")
	}
	full, ok := status.(interface{ StreamInfo() *jetstream.StreamInfo })
	if !ok || full.StreamInfo() == nil {
		return nil, fmt.Errorf("complete installed replay stream configuration required")
	}
	info := full.StreamInfo()
	c := info.Config
	if c.Name != "KV_"+sharedReplayBucket || len(c.Subjects) != 1 || c.Subjects[0] != "$KV."+sharedReplayBucket+".>" || len(c.Sources) != 0 || c.Mirror != nil || c.MirrorDirect || c.SubjectTransform != nil || c.RePublish != nil || c.AllowMsgTTL || c.Storage != jetstream.FileStorage || c.Retention != jetstream.LimitsPolicy || c.Discard != jetstream.DiscardNew || c.MaxMsgs != -1 || c.MaxBytes != -1 || c.MaxMsgsPerSubject != 1 || c.Replicas != 1 || c.NoAck || c.Sealed || c.MaxAge < 0 || c.MaxAge > 0 && c.MaxAge < 12*time.Hour || info.Created.IsZero() {
		return nil, fmt.Errorf("installed replay stream must match the isolated non-evicting file-backed bucket")
	}
	return info, nil
}

func prepareSharedReplayBucket(ctx context.Context, js sharedReplayInstaller, previous time.Time) (time.Time, error) {
	kv, err := js.KeyValue(ctx, sharedReplayBucket)
	if errors.Is(err, jetstream.ErrBucketNotFound) {
		if !previous.IsZero() {
			return time.Time{}, fmt.Errorf("installed replay bucket disappeared; refusing ledger recreation")
		}
		// The pinned library supplies DiscardNew, unlimited aggregate capacity,
		// fixed KV subjects, deny-delete, direct get and rollup semantics. Only
		// AccountInfo/INFO/CREATE are granted; its legacy UPDATE fallback is denied.
		kv, err = js.CreateKeyValue(ctx, jetstream.KeyValueConfig{Bucket: sharedReplayBucket, History: 1, TTL: 12 * time.Hour, Storage: jetstream.FileStorage, Replicas: 1, MaxBytes: -1, MaxValueSize: -1})
	}
	if err != nil || kv == nil {
		return time.Time{}, fmt.Errorf("fixed replay bucket provisioning refused")
	}
	info, err := sharedReplayStreamInfo(ctx, kv)
	if err != nil {
		return time.Time{}, err
	}
	if !previous.IsZero() && !info.Created.Equal(previous) {
		return time.Time{}, fmt.Errorf("installed replay bucket identity changed; refusing replay ledger replacement")
	}
	return info.Created, nil
}
