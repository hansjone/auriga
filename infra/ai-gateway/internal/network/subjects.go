// Package network holds the reusable pieces of Auriga's NATS ingress gate:
// the per-ingress subject layout, the auth callout that turns a one-time
// network ticket into a scoped NATS User JWT, and the system-account
// helpers used to kick or lame-duck a single connection.
//
// Design: docs/design/auriga-network (README 搂4.3/搂7.4, protocol 搂2).
// This package only builds the Broker-side boundary; per-operation IAM
// authorization stays in the adapter/collab path and is not done here.
package network

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"errors"
	"strings"
)

// Subject roots. Each ingress gets exactly one up subject (client -> adapter),
// one down subject (adapter -> client) and one private reply prefix used as the
// SDK inboxPrefix for request().
const (
	upRoot    = "auriga.net.up."
	downRoot  = "auriga.net.down."
	replyRoot = "auriga.net.reply."
)

// ingressEncoding is lowercase base32 without padding: only [a-z2-7], so an
// ingress is always a single NATS token and never a wildcard.
var ingressEncoding = base32.NewEncoding("abcdefghijklmnopqrstuvwxyz234567").WithPadding(base32.NoPadding)

const ingressBytes = 16

// ingressLen is the encoded length of a 16-byte ingress (128 bits).
var ingressLen = ingressEncoding.EncodedLen(ingressBytes)

var ErrInvalidIngress = errors.New("network: invalid ingress id")

// NewIngressID returns a fresh opaque, server-generated ingress id.
func NewIngressID() (string, error) {
	var b [ingressBytes]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return ingressEncoding.EncodeToString(b[:]), nil
}

// ValidIngressID reports whether id has the exact shape produced by NewIngressID.
func ValidIngressID(id string) bool {
	if len(id) != ingressLen {
		return false
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		if (c < 'a' || c > 'z') && (c < '2' || c > '7') {
			return false
		}
	}
	return true
}

// The shard is a routing hint only: 32 stable buckets, determined from ingress.
// It conveys no authority and must match the exact callout-issued subject.
func ingressShard(ingress string) string {
	digest := sha256.Sum256([]byte(ingress))
	return string("abcdefghijklmnopqrstuvwxyz234567"[digest[0]&31])
}
func UpSubject(ingress string) string   { return upRoot + ingressShard(ingress) + "." + ingress }
func DownSubject(ingress string) string { return downRoot + ingress }

// ReplyPrefix is the private inbox prefix handed to the SDK (nats.go
// CustomInboxPrefix / nats.js inboxPrefix). It has no trailing dot.
func ReplyPrefix(ingress string) string { return strings.TrimSuffix(replyRoot, ".") + "." + ingress }

// UpWildcard is the only subscription the adapter needs on the client side.
const UpWildcard = upRoot + "*.*"

// IngressFromUp extracts the ingress from a received up subject. The adapter
// must use this, never a tenant/node/ingress claimed inside the payload.
func IngressFromUp(subject string) (string, error) {
	if !strings.HasPrefix(subject, upRoot) {
		return "", ErrInvalidIngress
	}
	parts := strings.Split(subject[len(upRoot):], ".")
	if len(parts) != 2 || !ValidIngressID(parts[1]) || parts[0] != ingressShard(parts[1]) {
		return "", ErrInvalidIngress
	}
	return parts[1], nil
}

// ValidReplyFor reports whether reply is a concrete subject inside the private
// reply prefix of ingress, as produced by the SDK in mux (<prefix>.<nuid>.<tok>)
// or noMux (<prefix>.<nuid>) mode. The Broker does not restrict the reply
// field of a PUB, so the adapter must check it before responding: an empty or
// foreign reply is never answered.
func ValidReplyFor(ingress, reply string) bool {
	if !ValidIngressID(ingress) {
		return false
	}
	prefix := ReplyPrefix(ingress) + "."
	if !strings.HasPrefix(reply, prefix) {
		return false
	}
	rest := strings.Split(reply[len(prefix):], ".")
	if len(rest) < 1 || len(rest) > 2 {
		return false
	}
	for _, tok := range rest {
		if tok == "" || len(tok) > 64 {
			return false
		}
		for i := 0; i < len(tok); i++ {
			c := tok[i]
			if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (c < '0' || c > '9') {
				return false
			}
		}
	}
	return true
}
