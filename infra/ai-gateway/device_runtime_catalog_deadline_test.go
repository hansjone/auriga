package main

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hansjone/auriga/infra/ai-gateway/internal/admin"
	"github.com/hansjone/auriga/infra/ai-gateway/internal/budget"
	"github.com/hansjone/auriga/infra/ai-gateway/internal/modelgate"
	"github.com/hansjone/auriga/infra/ai-gateway/internal/relay"
	"github.com/hansjone/auriga/infra/ai-gateway/internal/relayhttp"
	"github.com/hansjone/auriga/infra/ai-gateway/internal/relaysupervisor"
	"github.com/hansjone/auriga/infra/ai-gateway/internal/store"
	"github.com/hansjone/auriga/infra/ai-gateway/internal/team"
	iam "github.com/hansjone/auriga/infra/gen/auriga/iam/v1"
	org "github.com/hansjone/auriga/infra/gen/auriga/org/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Replay expiry of the currently installed read deadline without waiting for a
// scheduler-dependent wall-clock delay. All reads and writes still use real TCP.
type deviceDeadlineConn struct {
	net.Conn
	mu       sync.Mutex
	deadline time.Time
}

func (c *deviceDeadlineConn) SetReadDeadline(d time.Time) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.deadline = d
	return c.Conn.SetReadDeadline(d)
}

func (c *deviceDeadlineConn) expireReadDeadline() (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.deadline.IsZero() {
		return false, nil
	}
	return true, c.Conn.SetReadDeadline(time.Now().Add(-time.Second))
}

type deviceDeadlineListener struct {
	net.Listener
	accepted chan *deviceDeadlineConn
}

func (l *deviceDeadlineListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	wrapped := &deviceDeadlineConn{Conn: c}
	l.accepted <- wrapped
	return wrapped, nil
}

// A missing member deliberately stops before any database query. This exercises
// the real handler, Authority and authenticated RPC transport without PostgreSQL;
// a read timeout must never replace the authoritative 403 with a dependency 503.
func TestDeviceShutdownQueryReadDeadlineIsolation(t *testing.T) {
	for _, path := range []string{"/ai/v1/models", "/api/ai/v1/me/usage"} {
		t.Run(path, func(t *testing.T) {
			entered, release, canceled := make(chan struct{}), make(chan struct{}), make(chan struct{})
			unblock := sync.OnceFunc(func() { close(release) })
			t.Cleanup(unblock)
			control := &deviceControl{tenant: "01900000-0000-7000-8000-000000000001"}
			claims := fmt.Sprintf(`{"tid":%q,"sub":%q,"did":%q,"exp":%d}`, control.tenant, deviceAccount, deviceID, time.Now().Add(2*time.Minute).Unix())
			control.raw = base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"ES256","typ":"at+jwt"}`)) + "." + base64.RawURLEncoding.EncodeToString([]byte(claims)) + ".ZmFrZQ"
			control.memberHook = func(ctx context.Context) error {
				close(entered)
				select {
				case <-release:
					return status.Error(codes.NotFound, "member absent")
				case <-ctx.Done():
					close(canceled)
					return ctx.Err()
				}
			}
			conn, err := team.DialRPC(deviceRPC(t, control, false))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = conn.Close() })
			iamConn, err := team.DialRPC(deviceRPC(t, control, true))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = iamConn.Close() })
			authority, err := modelgate.NewAuthority(iam.NewDeviceAccessServiceClient(iamConn), org.NewMemberServiceClient(conn), org.NewAuthorizationServiceClient(conn), org.NewQuotaServiceClient(conn), "USD", time.Now)
			if err != nil {
				t.Fatal(err)
			}
			reader, err := admin.NewRelayReader(new(store.Store), "USD", time.Now)
			if err != nil {
				t.Fatal(err)
			}
			gate, err := modelgate.New(authority, reader)
			if err != nil {
				t.Fatal(err)
			}
			sup, err := relaysupervisor.New(new(budget.Ledger), 1, time.Second, time.Millisecond)
			if err != nil {
				t.Fatal(err)
			}
			h, err := relayhttp.New(relayhttp.Config{Core: new(relay.Core), Gate: gate, Authority: authority, Ledger: new(budget.Ledger), ReserveRecovery: sup.Reserve, ReadTimeout: 100 * time.Millisecond, WriteTimeout: 100 * time.Millisecond, QueryTimeout: time.Second, MaxResponseBytes: 1 << 20, MaxInFlight: 1})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			r := &deviceRuntime{handler: h, supervisor: sup, cancel: cancel, closeResources: func() {}, shutdown: time.Second}
			r.server = &http.Server{Handler: h, BaseContext: func(net.Listener) context.Context { return ctx }}
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			l := &deviceDeadlineListener{Listener: listener, accepted: make(chan *deviceDeadlineConn, 1)}
			served := make(chan error, 1)
			go func() { served <- r.server.Serve(l) }()
			t.Cleanup(func() {
				unblock()
				cancel()
				_ = r.server.Close()
				_ = sup.Drain(context.Background())
				if err := <-served; !errors.Is(err, http.ErrServerClosed) {
					t.Error(err)
				}
			})
			client := &http.Client{Timeout: time.Second}
			t.Cleanup(client.CloseIdleConnections)
			type result struct {
				code int
				body string
				err  error
			}
			completed := make(chan result, 1)
			go func() {
				req, err := http.NewRequest("GET", "http://"+l.Addr().String()+path, nil)
				if err != nil {
					completed <- result{err: err}
					return
				}
				req.Header.Set("Authorization", "Bearer "+control.raw)
				res, err := client.Do(req)
				if err != nil {
					completed <- result{err: err}
					return
				}
				defer func() { _ = res.Body.Close() }()
				body, err := io.ReadAll(res.Body)
				completed <- result{code: res.StatusCode, body: string(body), err: err}
			}()
			select {
			case <-entered:
			case <-time.After(time.Second):
				t.Fatal("query did not enter member RPC")
			}
			active := <-l.accepted
			r.server.RegisterOnShutdown(func() {
				expired, err := active.expireReadDeadline()
				if err != nil {
					t.Error(err)
				}
				if expired {
					select {
					case <-canceled:
					case <-time.After(time.Second):
						t.Error("read deadline did not cancel member RPC")
					}
				}
				unblock()
			})
			if err := r.shutdownRuntime(); err != nil {
				t.Fatal(err)
			}
			got := <-completed
			if got.err != nil || got.code != http.StatusForbidden || !strings.Contains(got.body, `"code":"common.forbidden"`) {
				t.Fatalf("query read deadline replaced authority result: status=%d body=%s err=%v", got.code, got.body, got.err)
			}
		})
	}
}
