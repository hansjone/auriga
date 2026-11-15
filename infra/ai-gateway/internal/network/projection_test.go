package network

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestProjectedSignerSwitchAndCommittedFence(t *testing.T) {
	makeSigner := func(kid, version string) *errorSigner {
		_, key, e := ed25519.GenerateKey(rand.Reader)
		if e != nil {
			t.Fatal(e)
		}
		now := time.Now()
		return &errorSigner{key: key, tenant: "tenant-1", realm: "1", cfg: ProtocolSigningConfig{KeyID: kid, KeysetVersion: version}, begin: now.Add(-time.Minute), end: now.Add(time.Hour)}
	}
	old, next := makeSigner("old-protocol", "20"), makeSigner("new-protocol", "21")
	defer clear(old.key)
	defer clear(next.key)
	var allowed atomic.Bool
	allowed.Store(true)
	signer := &errorSigner{current: old, fence: func() error {
		if !allowed.Load() {
			return errors.New("committed fence")
		}
		return nil
	}}
	binding := liveBinding{Tenant: "tenant-1", Realm: "1", Node: "node-1", Ingress: "ingress-1", Incarnation: "incarnation-1", ChannelNonce: "nonce-1", Epoch: 1, NotAfter: time.Now().Add(time.Hour)}
	verify := func(expected *errorSigner) {
		t.Helper()
		raw, e := signer.encode(binding, "request-1", &AppError{Code: CodeProtocolError, Scope: "request"})
		if e != nil {
			t.Fatal(e)
		}
		var frame map[string]any
		if e = json.Unmarshal(raw, &frame); e != nil {
			t.Fatal(e)
		}
		auth, ok := frame["server_auth"].(map[string]any)
		if !ok {
			t.Fatal("missing server auth")
		}
		if auth["kid"] != expected.cfg.KeyID || auth["keyset_version"] != expected.cfg.KeysetVersion {
			t.Fatal("mixed snapshot")
		}
		sig, e := base64.RawURLEncoding.DecodeString(auth["signature"].(string))
		if e != nil {
			t.Fatal(e)
		}
		delete(auth, "signature")
		body, e := json.Marshal(frame)
		if e != nil {
			t.Fatal(e)
		}
		if !ed25519.Verify(expected.key.Public().(ed25519.PublicKey), append([]byte("Auriga.ServerEnvelope.v1\x00"), body...), sig) {
			t.Fatal("real protocol signature failed")
		}
	}
	verify(old)
	foreign := binding
	foreign.Realm = "2"
	if _, e := signer.encode(foreign, "request-1", &AppError{Code: CodeProtocolError, Scope: "request"}); e == nil {
		t.Fatal("cross-realm signing accepted")
	}
	foreign = binding
	foreign.Tenant = "tenant-2"
	if _, e := signer.encode(foreign, "request-1", &AppError{Code: CodeProtocolError, Scope: "request"}); e == nil {
		t.Fatal("cross-tenant signing accepted")
	}
	allowed.Store(false)
	if _, e := signer.encode(binding, "request-1", &AppError{Code: CodeProtocolError, Scope: "request"}); e == nil {
		t.Fatal("stale snapshot signed")
	}
	signer.mu.Lock()
	signer.current = next
	signer.mu.Unlock()
	allowed.Store(true)
	verify(next)
}
