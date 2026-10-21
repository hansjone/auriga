package runtime

import (
	"context"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/hansjone/auriga/infra/agent/internal/observer"
)

// A successful Engine start means only that a process exists. The existing
// Start context bounds the entire readiness wait; each HTTP attempt is shorter.
func waitInstanceHealth(ctx context.Context, endpoint string) error {
	base, err := url.Parse(endpoint)
	if err != nil {
		return err
	}
	transport := &http.Transport{
		Proxy:                 nil,
		DialContext:           (&net.Dialer{Timeout: 2 * time.Second}).DialContext,
		ResponseHeaderTimeout: 2 * time.Second,
		DisableKeepAlives:     true,
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 3 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	target := observer.InstanceTarget{BaseURL: base, Process: observer.ProcessRunning}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if observer.ProbeHealth(ctx, client, target).Ready {
			return ctx.Err()
		}
		timer := time.NewTimer(100 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}
