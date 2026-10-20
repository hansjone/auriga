// Package conn manages the NodeControl bidirectional session.
package conn

import (
	"context"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"encoding/pem"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"os"
	"runtime"
	"sync"
	"time"

	"github.com/hansjone/auriga/infra/agent/internal/enroll"
	"github.com/hansjone/auriga/infra/agent/internal/identity"
	"github.com/hansjone/auriga/infra/agent/internal/reconciler"
	"github.com/hansjone/auriga/infra/agent/internal/state"
	"github.com/hansjone/auriga/infra/agent/internal/tlsutil"
	nodev1 "github.com/hansjone/auriga/infra/gen/auriga/node/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Config configures the control session loop.
type Config struct {
	AgentDir        string
	AgentVersion    string
	CAFingerprint   string
	ControlURL      string // override; else from state meta
	IngressEndpoint string
	// Capacity is reported in Hello so the scheduler can place instances here.
	Capacity      *nodev1.Capacity
	Drivers       []nodev1.RuntimeKind
	BootID        string
	Logger        *slog.Logger
	Reconciler    *reconciler.Reconciler
	Store         *state.Store
	AllowInsecure bool

	HeartbeatInterval time.Duration
	MinBackoff        time.Duration
	MaxBackoff        time.Duration
	WelcomeTimeout    time.Duration
	// StableFor resets backoff only after a session stayed up this long (default 30s).
	StableFor time.Duration
	// RenewCheckEvery polls cert renewal (default 1h; tests use shorter).
	RenewCheckEvery time.Duration

	OnWelcome func(*nodev1.Welcome)
}

// Session runs Hello/Inventory/heartbeat and handles ApplySpec/Delete with reconnect.
type Session struct {
	cfg Config

	mu          sync.Mutex
	heartbeat   time.Duration
	lastWelcome *nodev1.Welcome
	welcomeAt   time.Time
	connected   bool
	statusCh    chan *nodev1.InstanceStatus
	usageCh     chan *nodev1.NodeUsage
	wakeCh      chan *nodev1.WakeSchedule
}

// New creates a session manager.
func New(cfg Config) *Session {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.MinBackoff <= 0 {
		cfg.MinBackoff = time.Second
	}
	if cfg.MaxBackoff <= 0 {
		cfg.MaxBackoff = 30 * time.Second
	}
	if cfg.HeartbeatInterval <= 0 {
		cfg.HeartbeatInterval = 15 * time.Second
	}
	if cfg.WelcomeTimeout <= 0 {
		cfg.WelcomeTimeout = 30 * time.Second
	}
	if cfg.StableFor <= 0 {
		cfg.StableFor = 30 * time.Second
	}
	if cfg.RenewCheckEvery <= 0 {
		cfg.RenewCheckEvery = time.Hour
	}
	if cfg.BootID == "" {
		cfg.BootID = fmt.Sprintf("boot-%d", time.Now().UnixNano())
	}
	return &Session{
		cfg:       cfg,
		heartbeat: cfg.HeartbeatInterval,
		statusCh:  make(chan *nodev1.InstanceStatus, 64),
		usageCh:   make(chan *nodev1.NodeUsage, 8),
		wakeCh:    make(chan *nodev1.WakeSchedule, 16),
	}
}

// StatusSink returns a sink that queues status for uplink.
func (s *Session) StatusSink() reconciler.StatusSink {
	return func(st *nodev1.InstanceStatus) {
		select {
		case s.statusCh <- st:
		default:
			s.cfg.Logger.Warn("status channel full", "instance_id", st.GetInstanceId())
		}
	}
}

// ReportUsage queues observer usage for the control stream (observer.Reporter).
func (s *Session) ReportUsage(u *nodev1.NodeUsage) {
	if u == nil {
		return
	}
	select {
	case s.usageCh <- u:
	default:
		select {
		case <-s.usageCh:
		default:
		}
		select {
		case s.usageCh <- u:
		default:
		}
	}
}

// ReportWake queues a wake schedule for the control stream (observer.Reporter).
func (s *Session) ReportWake(w *nodev1.WakeSchedule) {
	if w == nil {
		return
	}
	select {
	case s.wakeCh <- w:
	default:
		s.cfg.Logger.Warn("wake channel full", "instance_id", w.GetInstanceId())
	}
}

// ReportReady is accepted for observer.Reporter; ready flips travel via InstanceStatus.
func (s *Session) ReportReady(instanceID string, ready bool) {
	if s.cfg.Logger != nil {
		s.cfg.Logger.Debug("instance ready probe", "instance", instanceID, "ready", ready)
	}
}

// SetIngressEndpoint updates the Hello ingress_endpoint (set after ingress binds).
func (s *Session) SetIngressEndpoint(endpoint string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cfg.IngressEndpoint = endpoint
}

// Connected reports whether a Welcome has been received on the current stream.
func (s *Session) Connected() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.connected
}

// Run loops connect → session → backoff until ctx is done.
func (s *Session) Run(ctx context.Context) error {
	renewCtx, cancelRenew := context.WithCancel(ctx)
	renewDone := make(chan struct{})
	go func() {
		defer close(renewDone)
		s.renewLoop(renewCtx)
	}()
	defer func() {
		cancelRenew()
		<-renewDone
	}()

	backoff := s.cfg.MinBackoff
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		s.mu.Lock()
		s.lastWelcome = nil
		s.welcomeAt = time.Time{}
		s.mu.Unlock()

		err := s.runOnce(ctx)

		s.mu.Lock()
		hadWelcome := s.lastWelcome != nil
		stable := !s.welcomeAt.IsZero() && time.Since(s.welcomeAt) >= s.cfg.StableFor
		s.connected = false
		s.mu.Unlock()

		if ctx.Err() != nil {
			return ctx.Err()
		}
		wait := withJitter(backoff)
		s.cfg.Logger.Warn("session disconnected", "error", err, "backoff", wait.String())
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
		if hadWelcome && stable {
			backoff = s.cfg.MinBackoff
		} else {
			backoff *= 2
			if backoff > s.cfg.MaxBackoff {
				backoff = s.cfg.MaxBackoff
			}
		}
	}
}

func (s *Session) renewLoop(ctx context.Context) {
	// Only this goroutine renews; reconnects cannot bypass the retry delay.
	backoff := s.cfg.MinBackoff
	for {
		if ctx.Err() != nil {
			return
		}
		err := s.tryRenew(ctx)
		wait := s.cfg.RenewCheckEvery
		if err != nil {
			wait = withJitter(backoff)
			s.cfg.Logger.Error("certificate renew failed", "error", err, "next_backoff", wait.String())
			backoff *= 2
			if backoff > 30*time.Minute {
				backoff = 30 * time.Minute
			}
		} else {
			backoff = s.cfg.MinBackoff
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func (s *Session) tryRenew(ctx context.Context) error {
	opts := enroll.Options{
		AgentDir:      s.cfg.AgentDir,
		AgentVersion:  s.cfg.AgentVersion,
		CAFingerprint: s.cfg.CAFingerprint,
		AllowInsecure: s.cfg.AllowInsecure,
		Logger:        s.cfg.Logger,
		Store:         s.cfg.Store,
	}
	_, err := enroll.RenewCertificate(ctx, opts, false)
	return err
}

func (s *Session) runOnce(ctx context.Context) error {
	mat, meta, tlsCert, err := s.loadCreds()
	if err != nil {
		return err
	}
	endpoint := s.cfg.ControlURL
	if endpoint == "" {
		endpoint = meta.ControlEndpoint
	}
	if err := tlsutil.ValidateControlEndpoint(endpoint, s.cfg.AllowInsecure); err != nil {
		return err
	}
	fp := s.cfg.CAFingerprint
	if fp == "" {
		fp = meta.CAFingerprint
	}

	conn, err := tlsutil.Dial(ctx, tlsutil.DialOptions{
		Endpoint:          endpoint,
		CAFingerprint:     fp,
		CAPEM:             mat.CAPEM,
		ClientCertificate: &tlsCert,
		AllowInsecure:     s.cfg.AllowInsecure,
	})
	if err != nil {
		return fmt.Errorf("dial control: %w", err)
	}
	defer func() { _ = conn.Close() }()

	client := nodev1.NewNodeControlServiceClient(conn)
	stream, err := client.Session(ctx)
	if err != nil {
		return fmt.Errorf("open session: %w", err)
	}

	s.cfg.Logger.Info("control session opened", "endpoint", endpoint)
	if err := s.sendHello(stream, mat); err != nil {
		return err
	}
	s.cfg.Logger.Info("hello sent", "node_id", mat.NodeID)
	if err := s.sendInventory(stream); err != nil {
		return err
	}

	if s.cfg.Reconciler != nil {
		if err := s.cfg.Reconciler.ReconcileAll(ctx); err != nil {
			s.cfg.Logger.Warn("reconcile after connect", "error", err)
		}
		_ = s.sendInventory(stream)
	}

	welcome, err := s.recvWelcome(ctx, stream)
	if err != nil {
		return err
	}
	s.cfg.Logger.Info("welcome received; session established")
	s.mu.Lock()
	s.connected = true
	s.lastWelcome = welcome
	s.welcomeAt = time.Now()
	if iv := welcome.GetHeartbeatInterval(); iv != nil {
		d := iv.AsDuration()
		if d > 0 {
			s.heartbeat = d
		}
	} else {
		s.heartbeat = s.cfg.HeartbeatInterval
	}
	hb := s.heartbeat
	s.mu.Unlock()
	if s.cfg.OnWelcome != nil {
		s.cfg.OnWelcome(welcome)
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- s.recvLoop(ctx, stream)
	}()

	ticker := time.NewTicker(hb)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-errCh:
			return err
		case <-ticker.C:
			// Keepalive when observer has not produced a sample yet.
			select {
			case u := <-s.usageCh:
				if err := stream.Send(&nodev1.SessionRequest{
					Body: &nodev1.SessionRequest_Usage{Usage: u},
				}); err != nil {
					return err
				}
			default:
				if err := s.sendUsage(stream); err != nil {
					return err
				}
			}
		case u := <-s.usageCh:
			if err := stream.Send(&nodev1.SessionRequest{
				Body: &nodev1.SessionRequest_Usage{Usage: u},
			}); err != nil {
				return err
			}
		case w := <-s.wakeCh:
			if err := stream.Send(&nodev1.SessionRequest{
				Body: &nodev1.SessionRequest_Wake{Wake: w},
			}); err != nil {
				return err
			}
		case st := <-s.statusCh:
			if err := stream.Send(&nodev1.SessionRequest{
				Body: &nodev1.SessionRequest_Status{Status: st},
			}); err != nil {
				return err
			}
		}
	}
}

func (s *Session) loadCreds() (*identity.Material, state.NodeMeta, tls.Certificate, error) {
	mat, err := identity.Load(s.cfg.AgentDir)
	if err != nil {
		return nil, state.NodeMeta{}, tls.Certificate{}, err
	}
	if !mat.CertificateExpiresAt.IsZero() && time.Now().After(mat.CertificateExpiresAt) {
		return nil, state.NodeMeta{}, tls.Certificate{}, fmt.Errorf("node certificate expired")
	}
	meta, err := s.cfg.Store.GetNodeMeta()
	if err != nil {
		return nil, state.NodeMeta{}, tls.Certificate{}, err
	}
	fp := s.cfg.CAFingerprint
	if fp == "" {
		fp = meta.CAFingerprint
	}
	if fp == "" {
		return nil, state.NodeMeta{}, tls.Certificate{}, fmt.Errorf("ca fingerprint missing")
	}
	if err := tlsutil.VerifyCAPEMFingerprint(mat.CAPEM, fp); err != nil {
		return nil, state.NodeMeta{}, tls.Certificate{}, fmt.Errorf("ca.crt pin: %w", err)
	}
	mat.ControlEndpoint = meta.ControlEndpoint
	keyPEM, err := marshalKeyPEM(mat.PrivateKey)
	if err != nil {
		return nil, state.NodeMeta{}, tls.Certificate{}, err
	}
	tlsCert, err := tlsutil.LoadTLSCertificate(mat.CertificatePEM, keyPEM)
	if err != nil {
		return nil, state.NodeMeta{}, tls.Certificate{}, err
	}
	return mat, meta, tlsCert, nil
}

func (s *Session) sendHello(stream nodev1.NodeControlService_SessionClient, mat *identity.Material) error {
	hostname, _ := os.Hostname()
	s.mu.Lock()
	drivers := s.cfg.Drivers
	ingress := s.cfg.IngressEndpoint
	agentVersion := s.cfg.AgentVersion
	bootID := s.cfg.BootID
	capacity := s.cfg.Capacity
	s.mu.Unlock()
	if len(drivers) == 0 {
		drivers = []nodev1.RuntimeKind{nodev1.RuntimeKind_RUNTIME_KIND_UNSPECIFIED}
	}
	return stream.Send(&nodev1.SessionRequest{
		Body: &nodev1.SessionRequest_Hello{Hello: &nodev1.Hello{
			NodeId:          mat.NodeID,
			AgentVersion:    agentVersion,
			ProtocolVersion: 1,
			Os:              goos(),
			Arch:            runtime.GOARCH,
			Hostname:        hostname,
			Drivers:         drivers,
			BootId:          bootID,
			NodeTime:        timestamppb.Now(),
			SealingKeyId:    identity.SealingKeyID(mat.SealingPublic),
			IngressEndpoint: ingress,
			Capacity:        capacity,
		}},
	})
}

func (s *Session) sendInventory(stream nodev1.NodeControlService_SessionClient) error {
	var list []*nodev1.InstanceStatus
	if s.cfg.Reconciler != nil {
		var err error
		list, err = s.cfg.Reconciler.Inventory()
		if err != nil {
			return err
		}
	}
	return stream.Send(&nodev1.SessionRequest{
		Body: &nodev1.SessionRequest_Inventory{Inventory: &nodev1.Inventory{
			Instances: list,
		}},
	})
}

func (s *Session) sendUsage(stream nodev1.NodeControlService_SessionClient) error {
	return stream.Send(&nodev1.SessionRequest{
		Body: &nodev1.SessionRequest_Usage{Usage: &nodev1.NodeUsage{
			SampledAt: timestamppb.Now(),
			Node: &nodev1.NodeResources{
				CpuMillisUsed: 1,
				MemoryMbUsed:  64,
				DiskGbUsed:    1,
				DiskGbFree:    10,
			},
		}},
	})
}

func (s *Session) recvWelcome(ctx context.Context, stream nodev1.NodeControlService_SessionClient) (*nodev1.Welcome, error) {
	wctx, cancel := context.WithTimeout(ctx, s.cfg.WelcomeTimeout)
	defer cancel()
	type result struct {
		w   *nodev1.Welcome
		err error
	}
	ch := make(chan result, 1)
	go func() {
		for {
			msg, err := stream.Recv()
			if err != nil {
				ch <- result{err: err}
				return
			}
			if w := msg.GetWelcome(); w != nil {
				ch <- result{w: w}
				return
			}
			// Ignore Apply/Delete before Welcome.
			s.cfg.Logger.Warn("ignoring control message before Welcome")
		}
	}()
	select {
	case <-wctx.Done():
		return nil, fmt.Errorf("welcome timeout: %w", wctx.Err())
	case r := <-ch:
		return r.w, r.err
	}
}

func (s *Session) recvLoop(ctx context.Context, stream nodev1.NodeControlService_SessionClient) error {
	for {
		msg, err := stream.Recv()
		if err != nil {
			if err == io.EOF {
				return fmt.Errorf("session closed by server")
			}
			return err
		}
		if err := s.handleControl(ctx, msg); err != nil {
			s.cfg.Logger.Warn("handle control", "error", err)
		}
	}
}

func (s *Session) handleControl(ctx context.Context, msg *nodev1.SessionResponse) error {
	if msg == nil {
		return nil
	}
	s.cfg.Logger.Info("control message received", "type", fmt.Sprintf("%T", msg.Body))
	if s.cfg.Reconciler == nil {
		return nil
	}
	switch body := msg.Body.(type) {
	case *nodev1.SessionResponse_Apply:
		if body.Apply == nil || body.Apply.GetSpec() == nil {
			return nil
		}
		return s.cfg.Reconciler.ApplySpec(ctx, body.Apply.GetSpec())
	case *nodev1.SessionResponse_Delete:
		d := body.Delete
		if d == nil {
			return nil
		}
		return s.cfg.Reconciler.Delete(ctx, d.GetInstanceId(), d.GetGeneration(), d.GetPurgeData())
	case *nodev1.SessionResponse_Welcome:
		return nil
	default:
		return nil
	}
}

func withJitter(d time.Duration) time.Duration {
	if d <= 0 {
		return d
	}
	n, err := rand.Int(rand.Reader, big.NewInt(int64(d/2)+1))
	if err != nil {
		var b [8]byte
		_, _ = rand.Read(b[:])
		return d + time.Duration(binary.LittleEndian.Uint64(b[:])%uint64(d/2+1))
	}
	return d + time.Duration(n.Int64())
}

func goos() string {
	if runtime.GOOS == "windows" {
		return "windows"
	}
	return "linux"
}

func marshalKeyPEM(key *ecdsa.PrivateKey) ([]byte, error) {
	der, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der}), nil
}
