package main

import (
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	maxRateBuckets   = 10000
	bucketIdleExpire = 15 * time.Minute
)

// TokenBucketLimiter is a simple per-key token bucket.
type TokenBucketLimiter struct {
	mu      sync.Mutex
	rate    float64 // tokens per second
	burst   float64
	prefix  string
	buckets map[string]*bucket
	now     func() time.Time
}

type bucket struct {
	tokens float64
	last   time.Time
}

func NewTokenBucketLimiter(rps float64, burst int, pathPrefix string) *TokenBucketLimiter {
	if burst <= 0 {
		burst = 1
	}
	if rps < 0 {
		rps = 0
	}
	return &TokenBucketLimiter{
		rate:    rps,
		burst:   float64(burst),
		prefix:  pathPrefix,
		buckets: map[string]*bucket{},
		now:     time.Now,
	}
}

func (l *TokenBucketLimiter) Allow(key, path string) bool {
	if l == nil || l.rate == 0 && l.burst == 0 {
		return true
	}
	if l.prefix != "" && !strings.HasPrefix(path, l.prefix) {
		return true
	}
	key = rateLimitKey(key)
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	l.evictLocked(now)
	b, ok := l.buckets[key]
	if !ok {
		b = &bucket{tokens: l.burst, last: now}
		l.buckets[key] = b
	}
	elapsed := now.Sub(b.last).Seconds()
	if elapsed > 0 {
		b.tokens += elapsed * l.rate
		if b.tokens > l.burst {
			b.tokens = l.burst
		}
		b.last = now
	}
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

func (l *TokenBucketLimiter) evictLocked(now time.Time) {
	for k, b := range l.buckets {
		if b == nil || now.Sub(b.last) > bucketIdleExpire {
			delete(l.buckets, k)
		}
	}
	for len(l.buckets) >= maxRateBuckets {
		var oldestKey string
		var oldestTime time.Time
		first := true
		for k, b := range l.buckets {
			if b == nil {
				delete(l.buckets, k)
				continue
			}
			if first || b.last.Before(oldestTime) {
				oldestKey = k
				oldestTime = b.last
				first = false
			}
		}
		if oldestKey == "" {
			break
		}
		delete(l.buckets, oldestKey)
	}
}

func (l *TokenBucketLimiter) Middleware(next http.Handler, clientIP func(*http.Request) string) http.Handler {
	if l == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := clientIP(r)
		if ip == "" {
			ip = "unknown"
		}
		if !l.Allow(ip, r.URL.Path) {
			writeProblem(w, problem(http.StatusTooManyRequests, "edge.rate_limited", "Too Many Requests", "rate limit exceeded", requestID(r)))
			return
		}
		next.ServeHTTP(w, r)
	})
}
