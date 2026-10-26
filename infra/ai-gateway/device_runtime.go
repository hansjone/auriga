package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
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
)

type deviceRuntime struct {
	server         *http.Server
	handler        *relayhttp.Handler
	supervisor     *relaysupervisor.Supervisor
	cancel         context.CancelFunc
	closeResources func()
	shutdown       time.Duration
}

func newDeviceRuntime(ctx context.Context, c deviceConfig) (*deviceRuntime, error) {
	if err := validateDeviceConfig(c); err != nil {
		return nil, err
	}
	cipher, err := admin.LoadCipher(c.MasterKeyFile)
	if err != nil {
		return nil, errors.New("D7 master key unavailable")
	}
	db, err := store.Open(ctx, c.Store)
	if err != nil {
		return nil, errors.New("D7 store unavailable")
	}
	iamConn, err := team.DialRPC(team.RPCConfig(c.IAM))
	if err != nil {
		db.Close()
		return nil, errors.New("D7 IAM RPC unavailable")
	}
	orgConn, err := team.DialRPC(team.RPCConfig(c.Org))
	if err != nil {
		_ = iamConn.Close()
		db.Close()
		return nil, errors.New("D7 Org RPC unavailable")
	}
	closeResources := func() { _ = iamConn.Close(); _ = orgConn.Close(); db.Close() }
	reader, err := admin.NewRelayReader(db, "USD", time.Now)
	if err != nil {
		closeResources()
		return nil, err
	}
	a, err := modelgate.NewAuthority(iam.NewDeviceAccessServiceClient(iamConn), org.NewMemberServiceClient(orgConn), org.NewAuthorizationServiceClient(orgConn), org.NewQuotaServiceClient(orgConn), "USD", time.Now)
	if err != nil {
		closeResources()
		return nil, err
	}
	gate, err := modelgate.New(a, reader)
	if err != nil {
		closeResources()
		return nil, err
	}
	ledger, err := budget.New(db, "USD", nil)
	if err != nil {
		closeResources()
		return nil, err
	}
	transport, policy, err := deviceTransportPolicy(db, c)
	if err != nil {
		closeResources()
		return nil, err
	}
	limits := deviceProtocolLimits(c)
	core, err := relay.New(relay.Config{Gate: gate, Reader: reader, Cipher: cipher, Ledger: ledger, Policy: policy, Transport: transport, Limits: limits, RecheckInterval: 100 * time.Millisecond, CheckTimeout: time.Second, StopTimeout: 100 * time.Millisecond, AccountingTimeout: time.Second})
	if err != nil {
		closeResources()
		return nil, err
	}
	sup, err := relaysupervisor.New(ledger, c.Capacity, time.Second, 50*time.Millisecond)
	if err != nil {
		closeResources()
		return nil, err
	}
	h, err := relayhttp.New(relayhttp.Config{Core: core, Gate: gate, Authority: a, Ledger: ledger, ReserveRecovery: sup.Reserve, ReadTimeout: millis(c.ReadMillis), WriteTimeout: millis(c.WriteMillis), QueryTimeout: time.Second, MaxResponseBytes: limits.MaxBytes, MaxInFlight: c.MaxInFlight})
	if err != nil {
		_ = sup.Drain(context.Background())
		closeResources()
		return nil, err
	}
	// HTTP cancellation and recovery have independent lifetimes.
	httpCtx, cancel := context.WithCancel(context.Background())
	r := &deviceRuntime{handler: h, supervisor: sup, cancel: cancel, closeResources: closeResources, shutdown: millis(c.ShutdownMillis)}
	r.server = &http.Server{Handler: h, ReadHeaderTimeout: millis(c.HeaderMillis), ReadTimeout: millis(c.ReadMillis), WriteTimeout: 0, IdleTimeout: millis(c.IdleMillis), MaxHeaderBytes: 32768, BaseContext: func(net.Listener) context.Context { return httpCtx }}
	return r, nil
}

func (r *deviceRuntime) shutdownRuntime() error {
	r.handler.CloseAdmission()
	r.supervisor.CloseAdmission()
	ctx, cancel := context.WithTimeout(context.Background(), r.shutdown)
	err := r.server.Shutdown(ctx)
	cancel()
	r.cancel() // Give HTTP its grace period before cancelling remaining calls.
	if err != nil {
		_ = r.server.Close()
	}
	// Even after HTTP timeout, reserved cells count until their Execute finishes.
	drain, cancelDrain := context.WithTimeout(context.Background(), r.shutdown)
	drainErr := r.supervisor.Drain(drain)
	cancelDrain()
	stats := r.supervisor.Stats()
	if err != nil || drainErr != nil {
		// pool.Close waits for borrowed connections. With unresolved handlers or
		// cells that is unbounded here; leave resources for process exit instead.
		return fmt.Errorf("D7 shutdown incomplete: occupied=%d pending=%d faulted=%d", stats.Occupied, stats.Pending, stats.Faulted)
	}
	r.closeResources()
	return nil
}
func (r *deviceRuntime) serve(ctx context.Context, l net.Listener, maximum int) error {
	completed := make(chan error, 1)
	go func() { completed <- r.server.Serve(&deviceListener{Listener: l, slots: make(chan struct{}, maximum)}) }()
	var serveErr error
	select {
	case serveErr = <-completed:
	case <-ctx.Done():
	}
	err := r.shutdownRuntime()
	if serveErr == nil {
		serveErr = <-completed
	}
	if err != nil {
		return err
	}
	if errors.Is(serveErr, http.ErrServerClosed) {
		return nil
	}
	return serveErr
}

type deviceListener struct {
	net.Listener
	slots chan struct{}
}
type deviceConn struct {
	net.Conn
	once    sync.Once
	release func()
}

func (c *deviceConn) Close() error { err := c.Conn.Close(); c.once.Do(c.release); return err }
func (l *deviceListener) Accept() (net.Conn, error) {
	for {
		c, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		select {
		case l.slots <- struct{}{}:
			return &deviceConn{Conn: c, release: func() { <-l.slots }}, nil
		default:
			_ = c.Close()
		}
	}
}
func serveDevice(path string, logs io.Writer) error {
	c, err := loadDeviceConfig(path)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	startup, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	r, err := newDeviceRuntime(startup, c)
	if err != nil {
		return err
	}
	l, err := net.Listen("tcp", c.Listen)
	if err != nil {
		_ = r.shutdownRuntime()
		return errors.New("D7 listener unavailable")
	}
	defer func() { _ = l.Close() }()
	logger := slog.New(slog.NewJSONHandler(logs, nil))
	r.server.ErrorLog = slog.NewLogLogger(logger.Handler(), slog.LevelError)
	mode := "synthetic"
	if c.Upstream != nil {
		mode = "upstream"
	}
	logger.Info("D7 device listener", "address", l.Addr().String(), "mode", mode)
	return r.serve(ctx, l, c.MaxConnections)
}
