package main

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/nats-io/nats.go/jetstream"
	"strings"
	"time"
)

// NATSSharedEntryStore stores redemption and browser binding in one atomic KV Create.
// Provision a dedicated replicated bucket with MaxAge >= 12h (or no TTL), deny browser access.
type NATSSharedEntryStore struct{ KV jetstream.KeyValue }
type sharedStoredBinding struct {
	CookieHash string
	Binding    SharedBrowserBinding
}

func sharedCookieHash(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}
func (s *NATSSharedEntryStore) ConsumeAndBind(ctx context.Context, jti string, expiry time.Time, cookie string, b SharedBrowserBinding) error {
	if s == nil || s.KV == nil || !sharedUUID.MatchString(jti) || !strings.HasPrefix(cookie, jti+".") || !time.Now().Before(expiry) {
		return errors.New("common.unavailable")
	}
	status, err := s.KV.Status(ctx)
	if err != nil || status.TTL() > 0 && status.TTL() < 12*time.Hour {
		return errors.New("common.unavailable")
	}
	raw, err := json.Marshal(sharedStoredBinding{sharedCookieHash(cookie), b})
	if err != nil {
		return err
	}
	_, err = s.KV.Create(ctx, "ticket."+jti, raw)
	if errors.Is(err, jetstream.ErrKeyExists) {
		return errors.New("common.conflict")
	}
	return err
}
func (s *NATSSharedEntryStore) Lookup(ctx context.Context, cookie string) (SharedBrowserBinding, error) {
	var zero SharedBrowserBinding
	jti, _, ok := strings.Cut(cookie, ".")
	if !ok || !sharedUUID.MatchString(jti) {
		return zero, errors.New("shared.revoked")
	}
	if s == nil || s.KV == nil {
		return zero, errors.New("common.unavailable")
	}
	entry, err := s.KV.Get(ctx, "ticket."+jti)
	if errors.Is(err, jetstream.ErrKeyNotFound) {
		return zero, errors.New("shared.revoked")
	}
	if err != nil {
		return zero, err
	}
	var stored sharedStoredBinding
	d := json.NewDecoder(strings.NewReader(string(entry.Value())))
	d.UseNumber()
	if d.Decode(&stored) != nil {
		return zero, errors.New("common.unavailable")
	}
	if subtle.ConstantTimeCompare([]byte(stored.CookieHash), []byte(sharedCookieHash(cookie))) != 1 {
		return zero, errors.New("shared.revoked")
	}
	return stored.Binding, nil
}
