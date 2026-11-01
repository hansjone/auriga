package schemas

import "time"

type KVStore interface {
	Get(key string) (any, error)
	SetWithTTL(key string, value any, ttl time.Duration) error
	SetNXWithTTL(key string, value any, ttl time.Duration) (bool, error)
	Delete(key string) (bool, error)
}

const (
	DefaultSessionStickyTTL = time.Hour
)
