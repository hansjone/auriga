package network

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nkeys"
)

// The N0 spike runs a real nats-server v2.12.6 embedded in the test process:
// random ports, random test identities, every server and connection closed
// by t.Cleanup. Set AURIGA_N0_TRACE=1 to see the server log.

const clientAccount = "CLIENTS"

func randHex(t *testing.T, n int) string {
	t.Helper()
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(b)
}

// ---- one-time ticket store (stands in for the PG ticket table) ----

type ticketRec struct {
	ingress   string
	notAfter  time.Time
	expiresAt time.Time
	used      bool
}

type memTickets struct {
	mu       sync.Mutex
	recs     map[string]*ticketRec
	bindings []Attempt
	calls    int
	delay    time.Duration
	failWith error
}

func newMemTickets() *memTickets { return &memTickets{recs: map[string]*ticketRec{}} }

func (m *memTickets) issue(t *testing.T, notAfter time.Time) (ticket, ingress string) {
	t.Helper()
	ing, err := NewIngressID()
	if err != nil {
		t.Fatal(err)
	}
	tk := "nt_" + randHex(t, 24)
	m.mu.Lock()
	m.recs[tk] = &ticketRec{ingress: ing, notAfter: notAfter, expiresAt: time.Now().Add(30 * time.Second)}
	m.mu.Unlock()
	return tk, ing
}

var errTicket = errors.New("ticket rejected")

func (m *memTickets) Redeem(ctx context.Context, a Attempt) (Grant, error) {
	m.mu.Lock()
	m.calls++
	delay, fail := m.delay, m.failWith
	m.mu.Unlock()
	if delay > 0 {
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return Grant{}, ctx.Err()
		}
	}
	if fail != nil {
		return Grant{}, fail
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	r := m.recs[a.Ticket]
	if r == nil || r.used || time.Now().After(r.expiresAt) {
		return Grant{}, errTicket
	}
	// Consume and bind atomically (one critical section here, one PG tx in prod).
	r.used = true
	m.bindings = append(m.bindings, a)
	return Grant{Ingress: r.ingress, NotAfter: r.notAfter}, nil
}

func (m *memTickets) bindingFor(t *testing.T, ingress string) Binding {
	t.Helper()
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, a := range m.bindings {
		if r := m.recs[a.Ticket]; r != nil && r.ingress == ingress {
			return Binding{ServerID: a.ServerID, ClientID: a.ClientID, Account: clientAccount, Ingress: ingress}
		}
	}
	t.Fatalf("no binding for ingress %s", ingress)
	return Binding{}
}

func (m *memTickets) redeemCalls() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.calls
}

// ---- broker harness ----

type decision struct {
	a   Attempt
	uc  *jwt.UserClaims
	err error
}

type brokerOpts struct {
	authTimeout   float64 // seconds
	port          int     // -1 random
	wsPort        int     // -1 random
	maxTTL        time.Duration
	connTypes     []string
	limits        ClientLimits
	noCallout     bool // do not start the callout service
	noAcctLimits  bool // omit CLIENTS account limits
	redeemTimeout time.Duration
	// calloutAuthTimeout, when >0, is passed to CalloutConfig.AuthTimeout
	// instead of authTimeout. Used only by the deliberate mismatch evidence
	// fixture (server auth timeout stays small).
	calloutAuthTimeout time.Duration
	// tls enables a WSS listener with cert/key paths and optional origins.
	tls            bool
	tlsCertFile    string
	tlsKeyFile     string
	allowedOrigins []string
}

func defaultBrokerOpts() brokerOpts {
	return brokerOpts{
		authTimeout: 2,
		port:        -1,
		wsPort:      -1,
		maxTTL:      time.Hour,
		connTypes:   []string{jwt.ConnectionTypeWebsocket},
		limits:      ClientLimits{MaxPayload: 4096, MaxSubs: 8},
		// Must stay below auth_timeout, otherwise a late redemption can burn
		// a ticket for a connection the server already rejected.
		redeemTimeout: time.Second,
	}
}

type harness struct {
	t         *testing.T
	o         brokerOpts
	srv       *server.Server
	issuer    nkeys.KeyPair
	xkey      nkeys.KeyPair
	authUser  string
	authPass  string
	adptUser  string
	adptPass  string
	opsUser   string
	opsPass   string
	tickets   *memTickets
	decisions chan decision
	calloutNC *nats.Conn
}

func newHarness(t *testing.T, o brokerOpts) *harness {
	t.Helper()
	h := &harness{t: t, o: o, tickets: newMemTickets(), decisions: make(chan decision, 64)}
	var err error
	if h.issuer, err = nkeys.CreateAccount(); err != nil {
		t.Fatal(err)
	}
	if h.xkey, err = nkeys.CreateCurveKeys(); err != nil {
		t.Fatal(err)
	}
	h.authUser, h.authPass = "auth-"+randHex(t, 4), randHex(t, 16)
	h.adptUser, h.adptPass = "adapter-"+randHex(t, 4), randHex(t, 16)
	h.opsUser, h.opsPass = "ops-"+randHex(t, 4), randHex(t, 16)
	h.startServer()
	if !o.noCallout {
		h.startCallout()
	}
	return h
}

func (h *harness) config() string {
	issuerPub, _ := h.issuer.PublicKey()
	xPub, _ := h.xkey.PublicKey()
	acctLimits := ""
	if !h.o.noAcctLimits {
		// Per-connection limits for every client connection in the account;
		// this, not the User JWT, is what the server enforces in config mode.
		acctLimits = fmt.Sprintf("limits { max_payload: %d, max_subscriptions: %d }", h.o.limits.MaxPayload, h.o.limits.MaxSubs)
	}
	wsBlock := fmt.Sprintf(`websocket { host: "127.0.0.1", port: %d, no_tls: true }`, h.o.wsPort)
	if h.o.tls {
		origins := ""
		if len(h.o.allowedOrigins) > 0 {
			quoted := make([]string, len(h.o.allowedOrigins))
			for i, o := range h.o.allowedOrigins {
				quoted[i] = fmt.Sprintf("%q", o)
			}
			origins = fmt.Sprintf("allowed_origins: [%s]", strings.Join(quoted, ", "))
		}
		wsBlock = fmt.Sprintf(`websocket {
  host: "127.0.0.1"
  port: %d
  tls { cert_file: %q, key_file: %q }
  %s
}`, h.o.wsPort, h.o.tlsCertFile, h.o.tlsKeyFile, origins)
	}
	return fmt.Sprintf(`
server_name: "auriga-n0-%s"
host: "127.0.0.1"
port: %d
system_account: SYS
%s
accounts {
  AUTH { users: [ { user: %q, password: %q } ] }
  CLIENTS {
    %s
    exports: [ { stream: "auriga.net.up.>", accounts: [SVC] } ]
    imports: [
      { stream: { account: SVC, subject: "auriga.net.down.>" } }
      { stream: { account: SVC, subject: "auriga.net.reply.>" } }
    ]
  }
  SVC {
    users: [ { user: %q, password: %q, permissions: {
      publish: { allow: ["auriga.net.down.*", "auriga.net.reply.>", "auriga.internal.>"] }
      subscribe: { allow: ["auriga.net.up.*", "auriga.internal.>", "_INBOX.>"] }
    } } ]
    exports: [
      { stream: "auriga.net.down.>", accounts: [CLIENTS] }
      { stream: "auriga.net.reply.>", accounts: [CLIENTS] }
    ]
    imports: [ { stream: { account: CLIENTS, subject: "auriga.net.up.>" } } ]
  }
  SYS { users: [ { user: %q, password: %q, permissions: {
    publish: { allow: [%s] }
    subscribe: { allow: ["_INBOX.>"] }
  } } ] }
}
authorization {
  timeout: %v
  auth_callout {
    issuer: %q
    account: AUTH
    auth_users: [ %q, %q, %q ]
    xkey: %q
  }
}
`, randHex(h.t, 3), h.o.port, wsBlock,
		h.authUser, h.authPass, acctLimits, h.adptUser, h.adptPass, h.opsUser, h.opsPass,
		`"`+strings.Join(SysPublishAllow, `", "`)+`"`,
		h.o.authTimeout, issuerPub, h.authUser, h.adptUser, h.opsUser, xPub)
}

func (h *harness) startServer() {
	t := h.t
	path := filepath.Join(t.TempDir(), "nats.conf")
	if err := os.WriteFile(path, []byte(h.config()), 0o600); err != nil {
		t.Fatal(err)
	}
	opts, err := server.ProcessConfigFile(path)
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	opts.NoSigs = true
	trace := os.Getenv("AURIGA_N0_TRACE") != ""
	opts.NoLog = !trace
	opts.Debug = trace
	s, err := server.NewServer(opts)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	if trace {
		s.ConfigureLogger()
	}
	go s.Start()
	if !s.ReadyForConnections(10 * time.Second) {
		s.Shutdown()
		t.Fatal("nats-server not ready")
	}
	h.srv = s
	t.Cleanup(func() { h.stopServer() })
}

func (h *harness) stopServer() {
	if h.srv != nil {
		h.srv.Shutdown()
		h.srv.WaitForShutdown()
	}
}

func (h *harness) startCallout() {
	t := h.t
	authTO := time.Duration(h.o.authTimeout * float64(time.Second))
	if h.o.calloutAuthTimeout > 0 {
		authTO = h.o.calloutAuthTimeout
	}
	cfg := CalloutConfig{
		Issuer:          h.issuer,
		XKey:            h.xkey,
		Account:         clientAccount,
		MaxTTL:          h.o.maxTTL,
		Limits:          h.o.limits,
		ConnectionTypes: h.o.connTypes,
		Redeemer:        h.tickets,
		RedeemTimeout:   h.o.redeemTimeout,
		AuthTimeout:     authTO,
		OnDecision: func(a Attempt, uc *jwt.UserClaims, err error) {
			select {
			case h.decisions <- decision{a, uc, err}:
			default:
			}
		},
	}
	c, err := NewCallout(cfg)
	if err != nil {
		t.Fatal(err)
	}
	nc := h.internalConn(h.authUser, h.authPass)
	if _, err := c.Serve(nc); err != nil {
		t.Fatal(err)
	}
	if err := nc.Flush(); err != nil {
		t.Fatal(err)
	}
	h.calloutNC = nc
}

func (h *harness) tcpURL() string { return h.srv.ClientURL() }
func (h *harness) wsURL() string  { return h.srv.WebsocketURL() }

func (h *harness) internalConn(user, pass string) *nats.Conn {
	h.t.Helper()
	nc, err := nats.Connect(h.tcpURL(), nats.UserInfo(user, pass), nats.NoReconnect(), nats.Name(user))
	if err != nil {
		h.t.Fatalf("internal connect %s: %v", user, err)
	}
	h.t.Cleanup(nc.Close)
	return nc
}

func (h *harness) opsConn() *nats.Conn { return h.internalConn(h.opsUser, h.opsPass) }

// ---- DSH-side client ----

type client struct {
	nc      *nats.Conn
	ingress string
	errs    chan error
	closed  chan struct{}
	ldm     chan struct{}
}

func (c *client) expectAsyncErr(t *testing.T, contains string) {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		select {
		case err := <-c.errs:
			if strings.Contains(strings.ToLower(err.Error()), strings.ToLower(contains)) {
				return
			}
			t.Logf("other async error: %v", err)
		case <-deadline:
			t.Fatalf("no async error containing %q", contains)
		}
	}
}

func (c *client) noAsyncErr(t *testing.T) {
	t.Helper()
	select {
	case err := <-c.errs:
		t.Fatalf("unexpected async error: %v", err)
	default:
	}
}

// connect dials like a DSH would: one websocket connection, ticket as CONNECT
// auth_token, private inbox prefix, no automatic reconnect (the plugin
// supervisor redeems a new ticket instead).
func (h *harness) connect(url, ticket, ingress string, extra ...nats.Option) (*client, error) {
	c := &client{ingress: ingress, errs: make(chan error, 64), closed: make(chan struct{}), ldm: make(chan struct{}, 4)}
	opts := []nats.Option{
		nats.Token(ticket),
		nats.NoReconnect(),
		nats.Name("dsh-test"),
		nats.Timeout(5 * time.Second),
		nats.ErrorHandler(func(_ *nats.Conn, _ *nats.Subscription, err error) {
			select {
			case c.errs <- err:
			default:
			}
		}),
		nats.ClosedHandler(func(*nats.Conn) { close(c.closed) }),
		nats.LameDuckModeHandler(func(*nats.Conn) { c.ldm <- struct{}{} }),
	}
	if ingress != "" {
		opts = append(opts, nats.CustomInboxPrefix(ReplyPrefix(ingress)))
	}
	opts = append(opts, extra...)
	nc, err := nats.Connect(url, opts...)
	if err != nil {
		return nil, err
	}
	c.nc = nc
	h.t.Cleanup(nc.Close)
	return c, nil
}

// connectNew issues a fresh ticket and connects over websocket.
func (h *harness) connectNew(notAfter time.Time, extra ...nats.Option) *client {
	h.t.Helper()
	tk, ing := h.tickets.issue(h.t, notAfter)
	c, err := h.connect(h.wsURL(), tk, ing, extra...)
	if err != nil {
		h.t.Fatalf("connect: %v", err)
	}
	return c
}

func (h *harness) lastDecision() decision {
	h.t.Helper()
	select {
	case d := <-h.decisions:
		return d
	case <-time.After(3 * time.Second):
		h.t.Fatal("no callout decision")
		return decision{}
	}
}

// ---- adapter (internal service account) ----

type upMsg struct {
	ingress string
	subject string
	reply   string
	data    string
}

type adapter struct {
	nc        *nats.Conn
	got       chan upMsg
	rejected  chan upMsg
	mu        sync.Mutex
	noRespond map[string]bool
}

// startAdapter subscribes the up wildcard in SVC. It derives the ingress only
// from the subject and answers only replies inside that ingress' private prefix.
func (h *harness) startAdapter() *adapter {
	h.t.Helper()
	a := &adapter{nc: h.internalConn(h.adptUser, h.adptPass), got: make(chan upMsg, 64), rejected: make(chan upMsg, 64), noRespond: map[string]bool{}}
	_, err := a.nc.Subscribe(UpWildcard, func(m *nats.Msg) {
		ing, err := IngressFromUp(m.Subject)
		u := upMsg{ingress: ing, subject: m.Subject, reply: m.Reply, data: string(m.Data)}
		if err != nil {
			a.rejected <- u
			return
		}
		a.got <- u
		if m.Reply == "" {
			return
		}
		if !ValidReplyFor(ing, m.Reply) {
			a.rejected <- u
			return
		}
		a.mu.Lock()
		skip := a.noRespond[string(m.Data)]
		a.mu.Unlock()
		if !skip {
			_ = m.Respond([]byte("ack:" + ing))
		}
	})
	if err != nil {
		h.t.Fatal(err)
	}
	if err := a.nc.Flush(); err != nil {
		h.t.Fatal(err)
	}
	return a
}

func (a *adapter) expectUp(t *testing.T) upMsg {
	t.Helper()
	select {
	case u := <-a.got:
		return u
	case <-time.After(3 * time.Second):
		t.Fatal("adapter got nothing")
		return upMsg{}
	}
}

func (a *adapter) expectNoUp(t *testing.T, wait time.Duration) {
	t.Helper()
	select {
	case u := <-a.got:
		t.Fatalf("adapter unexpectedly got %+v", u)
	case <-time.After(wait):
	}
}

func waitClosed(t *testing.T, c *client, within time.Duration) time.Duration {
	t.Helper()
	start := time.Now()
	select {
	case <-c.closed:
		return time.Since(start)
	case <-time.After(within):
		t.Fatalf("connection not closed within %v", within)
		return 0
	}
}

func ctxT(t *testing.T) context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	t.Cleanup(cancel)
	return ctx
}
