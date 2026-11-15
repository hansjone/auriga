package ingress

import (
	"context"
	"encoding/binary"
	"errors"
	"go.etcd.io/bbolt"
)

// SharedIdentityReplayStore uses the node's protected control DB; the caller owns DB lifecycle.
// One durable bucket per ingress authority survives restart and serializes concurrent requests.
type SharedIdentityReplayStore struct{ DB *bbolt.DB }

func (s *SharedIdentityReplayStore) consume(ctx context.Context, jti string, expiresAt, now int64) error {
	if s == nil || s.DB == nil || ctx.Err() != nil {
		return errors.New("common.unavailable")
	}
	return s.DB.Update(func(tx *bbolt.Tx) error {
		if ctx.Err() != nil {
			return errors.New("common.unavailable")
		}
		b, err := tx.CreateBucketIfNotExists([]byte("shared-identity-v2-replay"))
		if err != nil {
			return err
		}
		cursor := b.Cursor()
		for k, v := cursor.First(); k != nil; k, v = cursor.Next() {
			if len(v) != 8 {
				return errors.New("common.unavailable")
			}
			if int64(binary.BigEndian.Uint64(v)) <= now {
				if err = cursor.Delete(); err != nil {
					return err
				}
			}
		}
		if b.Get([]byte(jti)) != nil {
			return errSharedInvalid
		}
		var value [8]byte
		binary.BigEndian.PutUint64(value[:], uint64(expiresAt))
		return b.Put([]byte(jti), value[:])
	})
}
