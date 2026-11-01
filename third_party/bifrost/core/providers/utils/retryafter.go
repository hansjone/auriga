// Auriga modifications: trimmed relay core/providers; see third_party/bifrost/UPSTREAM.md.

package utils

import (
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/maximhq/bifrost/core/schemas"
	"github.com/valyala/fasthttp"
)

const (
	minRetryAfter = time.Second
	maxRetryAfter = 5 * time.Minute
)

func ApplyRetryAfter(bifrostErr *schemas.BifrostError, headers *fasthttp.ResponseHeader) {
	if bifrostErr == nil || headers == nil || bifrostErr.ExtraFields.RetryAfter != 0 {
		return
	}
	header := func(name string) string {
		return strings.TrimSpace(string(headers.Peek(name)))
	}
	if hint, ok := retryAfterHint(header, time.Now()); ok {
		SetRetryAfter(bifrostErr, hint)
	}
}

func ApplyRetryAfterHTTP(bifrostErr *schemas.BifrostError, headers http.Header) {
	if bifrostErr == nil || headers == nil || bifrostErr.ExtraFields.RetryAfter != 0 {
		return
	}
	header := func(name string) string {
		return strings.TrimSpace(headers.Get(name))
	}
	if hint, ok := retryAfterHint(header, time.Now()); ok {
		SetRetryAfter(bifrostErr, hint)
	}
}

func SetRetryAfter(bifrostErr *schemas.BifrostError, hint time.Duration) {
	if bifrostErr == nil {
		return
	}
	bifrostErr.ExtraFields.RetryAfter = min(max(hint, minRetryAfter), maxRetryAfter).Milliseconds()
}

func retryAfterHint(header func(string) string, now time.Time) (time.Duration, bool) {
	if v := header("retry-after-ms"); v != "" {
		if ms, err := strconv.ParseFloat(v, 64); err == nil && !math.IsNaN(ms) {
			return scaledDuration(ms, time.Millisecond), true
		}
	}
	if v := header("Retry-After"); v != "" {
		if seconds, err := strconv.ParseFloat(v, 64); err == nil && !math.IsNaN(seconds) {
			return scaledDuration(seconds, time.Second), true
		}
		if at, err := http.ParseTime(v); err == nil {
			return at.Sub(now), true
		}
	}
	return 0, false
}

func scaledDuration(count float64, unit time.Duration) time.Duration {
	bound := float64(maxRetryAfter / unit)
	return time.Duration(min(max(count, 0), bound) * float64(unit))
}
