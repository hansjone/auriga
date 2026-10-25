package network

import (
	"bufio"
	"errors"
	"fmt"
	"net"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nkeys"
)

// Gate 1: one-time ticket -> scoped User JWT; replay, timeout and failure reject.

// rejected: over websocket nats.go v1.49.0 usually reports the
// server's rejection as EOF / connection reset instead of the -ERR text, so
// any connect error counts; callers also check the callout decision.
func rejected(err error) bool {
	return err != nil
}

func TestGate1_CalloutIssuesScopedUserJWT(t *testing.T) {
	h := newHarness(t, defaultBrokerOpts())
	ops := h.opsConn()
	notAfter := time.Now().Add(10 * time.Minute)
	c := h.connectNew(notAfter)
	d := h.lastDecision()
	if d.err != nil {
		t.Fatalf("callout rejected: %v", d.err)
	}
	uc := d.uc
	issuerPub, _ := h.issuer.PublicKey()

	if !nkeys.IsValidPublicUserKey(d.a.UserNkey) || uc.Subject != d.a.UserNkey {
		t.Fatalf("user JWT subject %q not bound to server user_nkey %q", uc.Subject, d.a.UserNkey)
	}
	if uc.Issuer != issuerPub || uc.Audience != clientAccount {
		t.Fatalf("issuer/audience = %q/%q", uc.Issuer, uc.Audience)
	}
	if uc.Expires != notAfter.Unix() {
		t.Fatalf("exp = %d, want grant NotAfter %d", uc.Expires, notAfter.Unix())
	}
	if d.a.ServerID != h.srv.ID() || d.a.ClientType != "websocket" || d.a.ClientID == 0 {
		t.Fatalf("attempt server/type/cid = %q/%q/%d", d.a.ServerID, d.a.ClientType, d.a.ClientID)
	}
	wantPub := []string{UpSubject(c.ingress)}
	wantSub := []string{DownSubject(c.ingress), ReplyPrefix(c.ingress) + ".>"}
	if !reflect.DeepEqual([]string(uc.Pub.Allow), wantPub) || !reflect.DeepEqual([]string(uc.Sub.Allow), wantSub) {
		t.Fatalf("permissions pub=%v sub=%v", uc.Pub.Allow, uc.Sub.Allow)
	}
	if uc.Resp != nil {
		t.Fatalf("allow_responses must be absent, got %+v", uc.Resp)
	}
	natsLim := uc.Limits.NatsLimits //nolint:staticcheck // QF1008: UserClaims.Payload() shadows promoted field
	if natsLim.Payload != 4096 || natsLim.Subs != 8 {
		t.Fatalf("limits payload=%d subs=%d", natsLim.Payload, natsLim.Subs)
	}
	if !reflect.DeepEqual([]string(uc.AllowedConnectionTypes), []string{jwt.ConnectionTypeWebsocket}) {
		t.Fatalf("connection types %v", uc.AllowedConnectionTypes)
	}
	// The server-side view must match what the callout bound.
	ci, err := LookupConn(ctxT(t), ops, d.a.ServerID, d.a.ClientID)
	if err != nil {
		t.Fatal(err)
	}
	if ci.Account != clientAccount || ci.AuthorizedUser != c.ingress {
		t.Fatalf("connz account=%q user=%q, want %q/%q", ci.Account, ci.AuthorizedUser, clientAccount, c.ingress)
	}
	t.Logf("server_id=%s cid=%d type=%s user_nkey=%s exp=%d", d.a.ServerID, d.a.ClientID, ci.Type, d.a.UserNkey, uc.Expires)
}

func TestGate1_TicketReplayRejected(t *testing.T) {
	h := newHarness(t, defaultBrokerOpts())
	tk, ing := h.tickets.issue(t, time.Now().Add(time.Hour))
	c1, err := h.connect(h.wsURL(), tk, ing)
	if err != nil {
		t.Fatal(err)
	}
	h.lastDecision()
	// Replay while the first connection is alive and after it closed.
	for _, phase := range []string{"while-open", "after-close"} {
		if phase == "after-close" {
			c1.nc.Close()
		}
		_, err := h.connect(h.wsURL(), tk, ing)
		if !rejected(err) {
			t.Fatalf("%s: replay err = %v", phase, err)
		}
		if d := h.lastDecision(); !errors.Is(d.err, errTicket) {
			t.Fatalf("%s: decision err = %v", phase, d.err)
		}
	}
	for name, ticket := range map[string]string{"unknown": "nt_" + randHex(t, 24), "empty": ""} {
		if _, err := h.connect(h.wsURL(), ticket, ""); !rejected(err) {
			t.Fatalf("%s ticket: err = %v", name, err)
		}
	}
}

func TestGate1_CalloutTimeoutAndFailureRejectNoFallback(t *testing.T) {
	t.Run("callout-service-absent", func(t *testing.T) {
		o := defaultBrokerOpts()
		o.authTimeout = 1
		o.noCallout = true
		h := newHarness(t, o)
		tk, ing := h.tickets.issue(t, time.Now().Add(time.Hour))
		start := time.Now()
		_, err := h.connect(h.wsURL(), tk, ing)
		if err == nil {
			t.Fatal("connected without callout service")
		}
		t.Logf("rejected after %v: %v", time.Since(start).Round(time.Millisecond), err)
	})
	t.Run("redeem-slower-than-auth-timeout", func(t *testing.T) {
		// Evidence: nats-server authorization.timeout is short while the
		// callout's RedeemTimeout is long — ticket can burn after the server
		// already rejected CONNECT. CalloutConfig.AuthTimeout is set large
		// only so NewCallout construction succeeds; it deliberately does NOT
		// match the server's auth timeout (production must keep them aligned).
		o := defaultBrokerOpts()
		o.authTimeout = 1
		o.redeemTimeout = 5 * time.Second
		o.calloutAuthTimeout = 10 * time.Second
		h := newHarness(t, o)
		h.tickets.delay = 2500 * time.Millisecond
		tk, ing := h.tickets.issue(t, time.Now().Add(time.Hour))
		start := time.Now()
		_, err := h.connect(h.wsURL(), tk, ing)
		if err == nil {
			t.Fatal("connected although callout answered after auth_timeout")
		}
		t.Logf("rejected after %v: %v", time.Since(start).Round(time.Millisecond), err)
	})
	t.Run("redeemer-error", func(t *testing.T) {
		h := newHarness(t, defaultBrokerOpts())
		h.tickets.failWith = errors.New("iam unavailable")
		tk, ing := h.tickets.issue(t, time.Now().Add(time.Hour))
		_, err := h.connect(h.wsURL(), tk, ing)
		if !rejected(err) {
			t.Fatalf("err = %v", err)
		}
		if d := h.lastDecision(); d.err == nil || d.uc != nil {
			t.Fatalf("decision = %+v", d)
		}
	})
	t.Run("no-static-fallback", func(t *testing.T) {
		h := newHarness(t, defaultBrokerOpts())
		h.tickets.failWith = errors.New("iam unavailable")
		before := h.tickets.redeemCalls()
		// auth_users bypass the callout but still need their password.
		for _, opt := range []nats.Option{
			nats.UserInfo(h.adptUser, "wrong"),
			nats.UserInfo(h.opsUser, ""),
			nats.UserInfo("anonymous", "x"),
		} {
			_, err := nats.Connect(h.wsURL(), opt, nats.NoReconnect())
			if !rejected(err) {
				t.Fatalf("err = %v", err)
			}
		}
		if _, err := nats.Connect(h.tcpURL(), nats.NoReconnect()); !rejected(err) {
			t.Fatalf("anonymous tcp err = %v", err)
		}
		// Credentials without a ticket never reach the redeemer.
		if got := h.tickets.redeemCalls() - before; got != 0 {
			t.Fatalf("redeem calls during fallback probes: %d", got)
		}
	})
}

func TestGate1_StandardTCPRejectedForWebsocketOnlyUser(t *testing.T) {
	h := newHarness(t, defaultBrokerOpts())
	tk, ing := h.tickets.issue(t, time.Now().Add(time.Hour))
	_, err := h.connect(h.tcpURL(), tk, ing)
	if err == nil {
		t.Fatal("websocket-only user connected over plain NATS TCP")
	}
	d := h.lastDecision()
	if d.err == nil || d.a.ClientType != "nats" {
		t.Fatalf("callout decision = %v type=%q", d.err, d.a.ClientType)
	}
	if h.tickets.redeemCalls() != 0 {
		t.Fatal("ticket consumed for a rejected transport")
	}
	// The ticket is still usable over the right transport.
	if _, err := h.connect(h.wsURL(), tk, ing); err != nil {
		t.Fatalf("websocket with same ticket: %v", err)
	}
	t.Logf("tcp rejected by callout: %v / client saw: %v", d.err, err)
}

// First failure, kept as evidence: the server itself ignores the User JWT's
// allowed_connection_types for callout users. If this starts failing, the
// upstream behaviour changed and the callout check becomes defense in depth.
func TestGate1_ServerIgnoresCalloutConnTypes_FirstFailure(t *testing.T) {
	o := defaultBrokerOpts()
	o.noCallout = true
	h := newHarness(t, o)
	h.forgedResponder(t, func(_ *jwt.AuthorizationRequestClaims, uc *jwt.UserClaims, _ *jwt.AuthorizationResponseClaims) (nkeys.KeyPair, bool) {
		uc.AllowedConnectionTypes = jwt.StringList{jwt.ConnectionTypeWebsocket}
		return h.issuer, true
	})
	c, err := h.connect(h.tcpURL(), "nt_x", "")
	if err != nil {
		t.Fatalf("upstream now enforces allowed_connection_types for callout users: %v", err)
	}
	t.Logf("CONFIRMED: WEBSOCKET-only callout user connected over TCP (connected=%v)", c.nc.IsConnected())
}

func TestGate1_UserLimitsEnforced(t *testing.T) {
	h := newHarness(t, defaultBrokerOpts())
	a := h.startAdapter()
	c := h.connectNew(time.Now().Add(time.Hour))
	for i := 0; i < 8; i++ {
		if _, err := c.nc.SubscribeSync(DownSubject(c.ingress)); err != nil {
			t.Fatal(err)
		}
	}
	_ = c.nc.Flush()
	c.noAsyncErr(t)
	if _, err := c.nc.SubscribeSync(DownSubject(c.ingress)); err != nil {
		t.Fatal(err)
	}
	_ = c.nc.Flush()
	c.expectAsyncErr(t, "maximum subscriptions exceeded")

	if err := c.nc.Publish(UpSubject(c.ingress), make([]byte, 4096)); err != nil {
		t.Fatal(err)
	}
	a.expectUp(t)
	// The account limit is advertised in INFO, so the SDK refuses locally.
	if mp := c.nc.MaxPayload(); mp != 4096 {
		t.Fatalf("advertised max_payload = %d", mp)
	}
	if err := c.nc.Publish(UpSubject(c.ingress), make([]byte, 4097)); !errors.Is(err, nats.ErrMaxPayload) {
		t.Fatalf("oversize publish err = %v", err)
	}
	a.expectNoUp(t, 300*time.Millisecond)
}

// A client that ignores the advertised limit is cut off by the server.
func TestGate1_AccountMaxPayloadEnforcedServerSide(t *testing.T) {
	o := defaultBrokerOpts()
	o.noCallout = true
	h := newHarness(t, o)
	h.forgedResponder(t, func(_ *jwt.AuthorizationRequestClaims, _ *jwt.UserClaims, _ *jwt.AuthorizationResponseClaims) (nkeys.KeyPair, bool) {
		return h.issuer, true
	})
	conn, err := net.Dial("tcp", strings.TrimPrefix(h.tcpURL(), "nats://"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	r := bufio.NewReader(conn)
	if _, err := r.ReadString('\n'); err != nil { // INFO
		t.Fatal(err)
	}
	_, _ = fmt.Fprintf(conn, "CONNECT {\"auth_token\":\"nt_raw\",\"verbose\":false,\"protocol\":1}\r\nPING\r\n")
	if line, _ := r.ReadString('\n'); !strings.HasPrefix(line, "PONG") {
		t.Fatalf("raw connect: %q", line)
	}
	_, _ = fmt.Fprintf(conn, "PUB auriga.net.up.x 4097\r\n%s\r\n", strings.Repeat("a", 4097))
	// Skip the INFO the server re-sends after CONNECT (connect_info), which
	// carries the per-connection max_payload of the account.
	var line string
	for line == "" || strings.HasPrefix(line, "INFO") {
		if strings.HasPrefix(line, "INFO") && !strings.Contains(line, `"max_payload":4096`) {
			t.Fatalf("INFO without account max_payload: %q", line)
		}
		if line, err = r.ReadString('\n'); err != nil {
			t.Fatal(err)
		}
	}
	if !strings.Contains(line, "Maximum Payload Violation") {
		t.Fatalf("server reply %q", line)
	}
	if _, err := r.ReadString('\n'); err == nil {
		t.Fatal("connection still open after payload violation")
	}
	t.Logf("server: %s", strings.TrimSpace(line))
}

// First failure, kept as evidence: User JWT max_payload/max_subs from the
// callout are not applied in config mode; only account limits are.
func TestGate1_UserJWTLimitsIgnored_FirstFailure(t *testing.T) {
	o := defaultBrokerOpts()
	o.noAcctLimits = true
	h := newHarness(t, o)
	a := h.startAdapter()
	c := h.connectNew(time.Now().Add(time.Hour))
	if d := h.lastDecision(); true {
		nl := d.uc.Limits.NatsLimits //nolint:staticcheck // QF1008: UserClaims.Payload() shadows promoted field
		if nl.Subs != 8 || nl.Payload != 4096 {
			t.Fatalf("JWT limits not set: %+v", nl)
		}
	}
	for i := 0; i < 9; i++ {
		if _, err := c.nc.SubscribeSync(DownSubject(c.ingress)); err != nil {
			t.Fatal(err)
		}
	}
	if err := c.nc.Publish(UpSubject(c.ingress), make([]byte, 8192)); err != nil {
		t.Fatal(err)
	}
	_ = c.nc.Flush()
	a.expectUp(t)
	c.noAsyncErr(t)
	if !c.nc.IsConnected() {
		t.Fatal("upstream now applies User JWT limits for callout users")
	}
	t.Log("CONFIRMED: 9 subs and an 8192-byte payload accepted despite JWT subs=8 payload=4096")
}

func TestGate1_GrantDeadlineAndMaxTTL(t *testing.T) {
	h := newHarness(t, defaultBrokerOpts())
	before := time.Now()
	h.connectNew(time.Now().Add(10 * time.Hour))
	d := h.lastDecision()
	if d.err != nil {
		t.Fatal(d.err)
	}
	if exp := time.Unix(d.uc.Expires, 0); exp.Before(before.Add(time.Hour-2*time.Second)) || exp.After(time.Now().Add(time.Hour)) {
		t.Fatalf("exp %v not clamped to MaxTTL 1h", exp)
	}
	tk, ing := h.tickets.issue(t, time.Now().Add(-time.Second))
	if _, err := h.connect(h.wsURL(), tk, ing); !rejected(err) {
		t.Fatalf("expired grant err = %v", err)
	}
	if d := h.lastDecision(); d.err == nil {
		t.Fatal("expired grant accepted by callout")
	}
}

// forgedResponder answers callout requests in place of the real service, so we
// can check which response defects nats-server itself rejects.
func (h *harness) forgedResponder(t *testing.T, mut func(req *jwt.AuthorizationRequestClaims, uc *jwt.UserClaims, rc *jwt.AuthorizationResponseClaims) (nkeys.KeyPair, bool)) {
	nc := h.internalConn(h.authUser, h.authPass)
	_, err := nc.Subscribe(AuthCalloutSubject, func(m *nats.Msg) {
		sx := m.Header.Get(AuthRequestXKeyHeader)
		plain, err := h.xkey.Open(m.Data, sx)
		if err != nil {
			return
		}
		req, err := jwt.DecodeAuthorizationRequestClaims(string(plain))
		if err != nil {
			return
		}
		ing, _ := NewIngressID()
		uc := ClientUserClaims(req.UserNkey, clientAccount, ing, time.Now().Add(time.Hour), ClientLimits{}, nil)
		rc := jwt.NewAuthorizationResponseClaims(req.UserNkey)
		rc.Audience = req.Server.ID
		signer, encrypt := mut(req, uc, rc)
		if rc.Jwt == "" {
			rc.Jwt, _ = uc.Encode(signer)
		}
		tok, _ := rc.Encode(signer)
		out := []byte(tok)
		if encrypt {
			out, _ = h.xkey.Seal(out, sx)
		}
		_ = m.Respond(out)
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = nc.Flush()
}

func TestGate1_ForgedCalloutResponsesRejected(t *testing.T) {
	otherAcc, _ := nkeys.CreateAccount()
	otherUser, _ := nkeys.CreateUser()
	otherUserPub, _ := otherUser.PublicKey()
	var replayed string
	cases := []struct {
		name   string
		accept bool
		mut    func(h *harness, req *jwt.AuthorizationRequestClaims, uc *jwt.UserClaims, rc *jwt.AuthorizationResponseClaims) (nkeys.KeyPair, bool)
	}{
		{"valid-control", true, func(h *harness, _ *jwt.AuthorizationRequestClaims, _ *jwt.UserClaims, _ *jwt.AuthorizationResponseClaims) (nkeys.KeyPair, bool) {
			return h.issuer, true
		}},
		{"plaintext-response-with-xkey", true, func(h *harness, _ *jwt.AuthorizationRequestClaims, _ *jwt.UserClaims, _ *jwt.AuthorizationResponseClaims) (nkeys.KeyPair, bool) {
			return h.issuer, false
		}},
		{"wrong-signing-account", false, func(h *harness, _ *jwt.AuthorizationRequestClaims, _ *jwt.UserClaims, _ *jwt.AuthorizationResponseClaims) (nkeys.KeyPair, bool) {
			return otherAcc, true
		}},
		{"wrong-server-audience", false, func(h *harness, _ *jwt.AuthorizationRequestClaims, _ *jwt.UserClaims, rc *jwt.AuthorizationResponseClaims) (nkeys.KeyPair, bool) {
			rc.Audience = "NBOGUSSERVER"
			return h.issuer, true
		}},
		{"user-jwt-for-other-nkey", false, func(h *harness, _ *jwt.AuthorizationRequestClaims, uc *jwt.UserClaims, _ *jwt.AuthorizationResponseClaims) (nkeys.KeyPair, bool) {
			uc.Subject = otherUserPub
			return h.issuer, true
		}},
		{"response-for-other-nkey", false, func(h *harness, _ *jwt.AuthorizationRequestClaims, _ *jwt.UserClaims, rc *jwt.AuthorizationResponseClaims) (nkeys.KeyPair, bool) {
			rc.Subject = otherUserPub
			return h.issuer, true
		}},
		{"expired-user-jwt", false, func(h *harness, _ *jwt.AuthorizationRequestClaims, uc *jwt.UserClaims, _ *jwt.AuthorizationResponseClaims) (nkeys.KeyPair, bool) {
			uc.Expires = time.Now().Add(-time.Minute).Unix()
			return h.issuer, true
		}},
		{"wrong-target-account", false, func(h *harness, _ *jwt.AuthorizationRequestClaims, uc *jwt.UserClaims, _ *jwt.AuthorizationResponseClaims) (nkeys.KeyPair, bool) {
			uc.Audience = "SVC_DOES_NOT_EXIST"
			return h.issuer, true
		}},
		{"replayed-user-jwt-from-earlier-connection", false, func(h *harness, _ *jwt.AuthorizationRequestClaims, uc *jwt.UserClaims, rc *jwt.AuthorizationResponseClaims) (nkeys.KeyPair, bool) {
			if replayed == "" {
				replayed, _ = uc.Encode(h.issuer)
				rc.Jwt = replayed
				return h.issuer, true // first connection is legit
			}
			rc.Jwt = replayed
			return h.issuer, true
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			o := defaultBrokerOpts()
			o.noCallout = true
			h := newHarness(t, o)
			h.forgedResponder(t, func(req *jwt.AuthorizationRequestClaims, uc *jwt.UserClaims, rc *jwt.AuthorizationResponseClaims) (nkeys.KeyPair, bool) {
				return tc.mut(h, req, uc, rc)
			})
			if tc.name == "replayed-user-jwt-from-earlier-connection" {
				if _, err := h.connect(h.wsURL(), "nt_first", ""); err != nil {
					t.Fatalf("first connection: %v", err)
				}
			}
			_, err := h.connect(h.wsURL(), "nt_"+randHex(t, 8), "")
			if tc.accept && err != nil {
				t.Fatalf("control rejected: %v", err)
			}
			if !tc.accept && err == nil {
				t.Fatal("forged response accepted")
			}
			t.Logf("result: %v", err)
		})
	}
}
