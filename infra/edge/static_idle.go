package main

import (
	"context"
	"io"
	"net/http"
	"time"
)

// Bound only an outstanding upstream Read. A slow client write does not spend
// this budget, and each successful upstream read gets a fresh idle interval.
type staticIdleTransport struct {
	base    http.RoundTripper
	timeout time.Duration
}

func (t staticIdleTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if !staticAssetPath(r) && r.URL.Path != liteLLMUIConfigPath {
		return t.base.RoundTrip(r)
	}
	ctx, cancel := context.WithCancel(r.Context())
	resp, err := t.base.RoundTrip(r.WithContext(ctx))
	if err != nil {
		cancel()
		return nil, err
	}
	resp.Body = &staticIdleBody{ReadCloser: resp.Body, cancel: cancel, timeout: t.timeout}
	return resp, nil
}

type staticIdleBody struct {
	io.ReadCloser
	cancel  context.CancelFunc
	timeout time.Duration
}

func (b *staticIdleBody) Read(p []byte) (int, error) {
	timer := time.AfterFunc(b.timeout, b.cancel)
	n, err := b.ReadCloser.Read(p)
	timer.Stop()
	return n, err
}
func (b *staticIdleBody) Close() error { b.cancel(); return b.ReadCloser.Close() }
