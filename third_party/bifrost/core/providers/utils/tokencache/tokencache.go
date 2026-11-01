// Auriga modifications: trimmed relay core/providers; see third_party/bifrost/UPSTREAM.md.

package tokencache

import (
	"context"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	providerUtils "github.com/maximhq/bifrost/core/providers/utils"
	schemas "github.com/maximhq/bifrost/core/schemas"
)

type failure struct {
	err        *schemas.BifrostError
	retryAfter time.Time
	attempts   int
}

var DefaultPermanentBackoff = Backoff{Base: 2 * time.Second, Cap: 60 * time.Second}

type Entry[T any] struct {
	Value     T
	ExpiresAt time.Time
	RefreshAt time.Time
}
type Minter[T any] func(ctx context.Context, prev *Entry[T]) (*Entry[T], *schemas.BifrostError)

var DefaultTransientBackoff = Backoff{Base: 500 * time.Millisecond, Cap: 10 * time.Second}

type Options struct {
	ExchangeTimeout time.Duration
	Permanent       Backoff
	Transient       Backoff
	IsPermanent     func(*schemas.BifrostError) bool
	Now             func() time.Time
}
type Cache[T any] struct {
	slots sync.Map
	opts  Options
}
type slot[T any] struct {
	mu      sync.Mutex
	entry   atomic.Pointer[Entry[T]]
	failure atomic.Pointer[failure]
	gen     atomic.Uint64
}
type Backoff struct {
	Base time.Duration
	Cap  time.Duration
}

var fallbackExchangeClient = &http.Client{Timeout: DefaultExchangeTimeout}

const (
	DefaultExchangeTimeout = 20 * time.Second

	DefaultRefreshMargin = 60 * time.Second
)

func New[T any](opts Options) *Cache[T] {
	if opts.ExchangeTimeout <= 0 {
		opts.ExchangeTimeout = DefaultExchangeTimeout
	}
	if opts.Permanent == (Backoff{}) {
		opts.Permanent = DefaultPermanentBackoff
	}
	if opts.Transient == (Backoff{}) {
		opts.Transient = DefaultTransientBackoff
	}
	if opts.IsPermanent == nil {
		opts.IsPermanent = IsPermanentError
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	return &Cache[T]{opts: opts}
}

func (c *Cache[T]) Get(ctx context.Context, cacheKey string, mint Minter[T]) (*Entry[T], *schemas.BifrostError) {
	s := c.slot(cacheKey)
	now := c.opts.Now()

	e := s.entry.Load()
	if fresh(e, now) {
		return e, nil
	}

	if f := s.failure.Load(); f != nil && now.Before(f.retryAfter) {
		if usable(e, now) {
			return e, nil
		}
		return nil, copyError(f.err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	now = c.opts.Now()
	e = s.entry.Load()
	if fresh(e, now) {
		return e, nil
	}
	if f := s.failure.Load(); f != nil && now.Before(f.retryAfter) {
		if usable(e, now) {
			return e, nil
		}
		return nil, copyError(f.err)
	}

	if ctx == nil {
		ctx = context.Background()
	}
	exchangeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), c.opts.ExchangeTimeout)
	defer cancel()

	gen := s.gen.Load()
	prev := e
	e, bErr := mint(exchangeCtx, prev)
	if bErr == nil && e != nil && prev != nil && s.gen.Load() != gen {

		prev = nil
		e, bErr = mint(exchangeCtx, nil)
	}
	if bErr == nil && e == nil {
		bErr = providerUtils.NewConfigurationError("token minter returned neither a token nor an error")
	}
	if bErr != nil {
		permanent := c.opts.IsPermanent(bErr)
		s.recordFailure(bErr, permanent, c)

		if !permanent && s.gen.Load() == gen && usable(prev, c.opts.Now()) {
			return prev, nil
		}
		s.entry.Store(nil)
		return nil, copyError(bErr)
	}
	if e.RefreshAt.IsZero() {
		e.RefreshAt = e.ExpiresAt
	}
	s.entry.Store(e)
	s.failure.Store(nil)
	return e, nil
}

func (c *Cache[T]) Peek(cacheKey string) *Entry[T] {
	if s, ok := c.slots.Load(cacheKey); ok {
		return s.(*slot[T]).entry.Load()
	}
	return nil
}

func (c *Cache[T]) slot(cacheKey string) *slot[T] {
	if existing, ok := c.slots.Load(cacheKey); ok {
		return existing.(*slot[T])
	}
	actual, _ := c.slots.LoadOrStore(cacheKey, &slot[T]{})
	return actual.(*slot[T])
}

func fresh[T any](e *Entry[T], now time.Time) bool {
	return e != nil && now.Before(e.RefreshAt)
}

func usable[T any](e *Entry[T], now time.Time) bool {
	return e != nil && now.Before(e.ExpiresAt)
}

func copyError(bErr *schemas.BifrostError) *schemas.BifrostError {
	if bErr == nil {
		return nil
	}
	c := *bErr
	if bErr.Error != nil {
		field := *bErr.Error
		c.Error = &field
	}
	return &c
}

func (s *slot[T]) recordFailure(bErr *schemas.BifrostError, permanent bool, c *Cache[T]) {
	attempts := 1
	if prev := s.failure.Load(); prev != nil {
		attempts = prev.attempts + 1
	}
	backoff := c.opts.Transient
	if permanent {
		backoff = c.opts.Permanent
	}
	delay := backoff.For(attempts)
	if hinted := time.Duration(bErr.ExtraFields.RetryAfter) * time.Millisecond; hinted > delay {
		delay = hinted
	}
	s.failure.Store(&failure{
		err:        bErr,
		retryAfter: c.opts.Now().Add(delay),
		attempts:   attempts,
	})
}

func (b Backoff) For(attempts int) time.Duration {
	delay := b.Base
	for i := 1; i < attempts && delay < b.Cap; i++ {
		delay *= 2
	}
	if delay > b.Cap {
		delay = b.Cap
	}
	return delay
}

func exchangeClient(hc *http.Client) *http.Client {
	if hc != nil {
		return hc
	}
	return fallbackExchangeClient
}

func IsPermanentError(bErr *schemas.BifrostError) bool {
	if bErr == nil || bErr.StatusCode == nil {
		return false
	}
	switch status := *bErr.StatusCode; {
	case status == 0, status == http.StatusTooManyRequests, status >= 500:
		return false
	default:
		return true
	}
}
