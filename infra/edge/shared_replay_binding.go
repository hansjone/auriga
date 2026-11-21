package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

// Deliberately has no stream/bucket create, update or delete operation. The
// installation phase owns provisioning; the resident Edge credential binds only.
type sharedReplayBucketReader interface {
	KeyValue(context.Context, string) (jetstream.KeyValue, error)
}

func bindSharedReplayBucket(ctx context.Context, reader sharedReplayBucketReader) (jetstream.KeyValue, error) {
	kv, err := reader.KeyValue(ctx, sharedEntryBucket)
	if errors.Is(err, jetstream.ErrBucketNotFound) {
		return nil, fmt.Errorf("shared replay bucket is not installed; run the public shared-replay-prepare installation step")
	}
	if err != nil || kv == nil {
		return nil, fmt.Errorf("installed shared replay bucket unavailable")
	}
	status, err := kv.Status(ctx)
	if err != nil || status == nil {
		return nil, fmt.Errorf("installed shared replay bucket status unavailable")
	}
	// The pinned nats.go KeyValueBucketStatus exposes the underlying complete
	// StreamInfo. KeyValueConfig alone drops Subjects/subject transforms and is
	// insufficient to reject a same-name stream sourcing another private bucket.
	full, ok := status.(interface{ StreamInfo() *jetstream.StreamInfo })
	if !ok {
		return nil, fmt.Errorf("complete installed shared replay stream configuration required")
	}
	info := full.StreamInfo()
	if info == nil {
		return nil, fmt.Errorf("complete installed shared replay stream configuration required")
	}
	if err := validateSharedReplayStream(info.Config); err != nil {
		return nil, err
	}
	return kv, nil
}

func validateSharedReplayStream(config jetstream.StreamConfig) error {
	// Match the pinned CreateKeyValue configuration. Capacity pressure must
	// reject a new ticket, never evict an earlier replay tombstone before TTL.
	if config.Name != "KV_"+sharedEntryBucket || len(config.Subjects) != 1 || config.Subjects[0] != "$KV."+sharedEntryBucket+".>" || len(config.Sources) != 0 || config.Mirror != nil || config.MirrorDirect || config.SubjectTransform != nil || config.RePublish != nil || config.AllowMsgTTL || config.Storage != jetstream.FileStorage || config.Retention != jetstream.LimitsPolicy || config.Discard != jetstream.DiscardNew || config.MaxMsgs != -1 || config.MaxBytes != -1 || config.MaxMsgsPerSubject != 1 || config.Replicas != 1 || config.NoAck || config.Sealed || config.MaxAge < 0 || config.MaxAge > 0 && config.MaxAge < 12*time.Hour {
		return fmt.Errorf("installed shared replay stream is not the isolated file-backed replay bucket")
	}
	return nil
}
