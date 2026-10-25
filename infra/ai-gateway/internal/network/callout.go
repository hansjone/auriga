package network

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nkeys"
)

// Fixed nats-server v2.12.6 protocol names (server/auth_callout.go:30-32).
const (
	AuthCalloutSubject    = "$SYS.REQ.USER.AUTH"
	AuthRequestXKeyHeader = "Nats-Server-Xkey"
)

// Attempt is what the callout learned from the server for one CONNECT. The
// Redeemer must consume the ticket and record the binding
// (ServerID, ClientID, UserNkey) -> ingress in one atomic step, so that later
// kicks can be verified against the authoritative record.
type Attempt struct {
	Ticket     string
	ServerID   string // nats server public key; changes on every server start
	ServerName string
	ClientID   uint64 // CID, local to ServerID
	ClientType string // "websocket", "nats", ...
	UserNkey   string // nkey the server generated for this connection
	At         time.Time
}

// Grant is the authoritative result of redeeming a ticket.
type Grant struct {
	Ingress string
	// NotAfter is the earliest of the IAM session, device and instance
	// authorization deadlines. The User JWT never outlives it.
	NotAfter time.Time
}

// Redeemer consumes one-time network tickets. It must reject unknown,
// expired and already-used tickets and must be safe for concurrent use.
type Redeemer interface {
	Redeem(ctx context.Context, a Attempt) (Grant, error)
}

// Limits applied to every client User JWT.
type ClientLimits struct {
	MaxPayload int64
	MaxSubs    int64
}

type CalloutConfig struct {
	// Issuer is the account key configured as auth_callout.issuer; it signs
	// both the User JWT and the authorization response.
	Issuer nkeys.KeyPair
	// XKey is the callout's curve key (auth_callout.xkey). When set, requests
	// must be encrypted; plaintext requests are refused.
	XKey nkeys.KeyPair
	// Account the client is placed in (config mode: User JWT audience).
	Account string
	// MaxTTL caps the User JWT lifetime (design default: 1h).
	MaxTTL time.Duration
	// Limits are written into the User JWT, but nats-server v2.12.6 config
	// mode does not apply them to callout users (processReply clears
	// c.opts.JWT before applyAccountLimits reads it). The enforced limits
	// must be set as account limits on the client account.
	Limits ClientLimits
	// ConnectionTypes restricts the transport, e.g. jwt.ConnectionTypeWebsocket.
	ConnectionTypes []string
	Redeemer        Redeemer
	RedeemTimeout   time.Duration
	Now             func() time.Time
	// OnDecision, if set, observes every decision. The error is internal
	// detail and must not be sent to the client.
	OnDecision func(a Attempt, claims *jwt.UserClaims, err error)
}

// Callout answers nats-server auth callout requests.
type Callout struct {
	cfg       CalloutConfig
	issuerPub string
}

var errUnauthorized = errors.New("not authorized")

func NewCallout(cfg CalloutConfig) (*Callout, error) {
	if cfg.Issuer == nil || cfg.Redeemer == nil || cfg.Account == "" {
		return nil, errors.New("network: callout needs issuer, redeemer and account")
	}
	pub, err := cfg.Issuer.PublicKey()
	if err != nil || !nkeys.IsValidPublicAccountKey(pub) {
		return nil, errors.New("network: callout issuer must be an account key")
	}
	if cfg.MaxTTL <= 0 {
		cfg.MaxTTL = time.Hour
	}
	if cfg.RedeemTimeout <= 0 {
		cfg.RedeemTimeout = time.Second
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &Callout{cfg: cfg, issuerPub: pub}, nil
}

// Serve subscribes the callout on an auth_users connection in the callout account.
func (c *Callout) Serve(nc *nats.Conn) (*nats.Subscription, error) {
	return nc.Subscribe(AuthCalloutSubject, func(m *nats.Msg) {
		resp, ok := c.Handle(m.Header, m.Data)
		if ok && m.Reply != "" {
			_ = m.Respond(resp)
		}
	})
}

// Handle processes one raw request. ok=false means the request could not be
// attributed to a connection; no answer is sent and the server times out,
// which also rejects the client.
func (c *Callout) Handle(hdr nats.Header, data []byte) (resp []byte, ok bool) {
	serverXKey := hdr.Get(AuthRequestXKeyHeader)
	if c.cfg.XKey != nil {
		if serverXKey == "" {
			return nil, false
		}
		plain, err := c.cfg.XKey.Open(data, serverXKey)
		if err != nil {
			return nil, false
		}
		data = plain
	}
	req, err := jwt.DecodeAuthorizationRequestClaims(string(data))
	if err != nil {
		return nil, false
	}
	vr := jwt.CreateValidationResults()
	req.Validate(vr)
	// The request is signed with the server's own key, whose public key is its ID.
	if len(vr.Errors()) > 0 || req.Issuer != req.Server.ID || req.UserNkey == "" {
		return nil, false
	}

	a := Attempt{
		Ticket:     req.ConnectOptions.Token,
		ServerID:   req.Server.ID,
		ServerName: req.Server.Name,
		ClientID:   req.ClientInformation.ID,
		ClientType: req.ClientInformation.Type,
		UserNkey:   req.UserNkey,
		At:         c.cfg.Now(),
	}
	uc, err := c.authorize(a)
	rc := jwt.NewAuthorizationResponseClaims(req.UserNkey)
	rc.Audience = req.Server.ID
	if err == nil {
		rc.Jwt, err = uc.Encode(c.cfg.Issuer)
	}
	if err != nil {
		uc, rc.Jwt, rc.Error = nil, "", errUnauthorized.Error()
	}
	if c.cfg.OnDecision != nil {
		c.cfg.OnDecision(a, uc, err)
	}
	token, err := rc.Encode(c.cfg.Issuer)
	if err != nil {
		return nil, false
	}
	if c.cfg.XKey != nil {
		sealed, err := c.cfg.XKey.Seal([]byte(token), serverXKey)
		if err != nil {
			return nil, false
		}
		return sealed, true
	}
	return []byte(token), true
}

func (c *Callout) authorize(a Attempt) (*jwt.UserClaims, error) {
	if a.Ticket == "" {
		return nil, errors.New("missing ticket")
	}
	// nats-server v2.12.6 does not enforce allowed_connection_types for
	// callout-issued users (auth_callout.go processReply never calls
	// connectionTypeAllowed), so the callout must check the transport itself,
	// before the ticket is consumed.
	if !transportAllowed(c.cfg.ConnectionTypes, a.ClientType) {
		return nil, fmt.Errorf("transport %q not allowed", a.ClientType)
	}
	ctx, cancel := context.WithTimeout(context.Background(), c.cfg.RedeemTimeout)
	defer cancel()
	g, err := c.cfg.Redeemer.Redeem(ctx, a)
	if err != nil {
		return nil, fmt.Errorf("redeem: %w", err)
	}
	if !ValidIngressID(g.Ingress) {
		return nil, ErrInvalidIngress
	}
	now := c.cfg.Now()
	exp := now.Add(c.cfg.MaxTTL)
	if !g.NotAfter.IsZero() && g.NotAfter.Before(exp) {
		exp = g.NotAfter
	}
	// JWT exp has one-second resolution; anything under a second is expired.
	if exp.Unix() <= now.Unix() {
		return nil, errors.New("grant already expired")
	}
	return ClientUserClaims(a.UserNkey, c.cfg.Account, g.Ingress, exp, c.cfg.Limits, c.cfg.ConnectionTypes), nil
}

// ClientPermissions is the exact ACL of one ingress: publish only to its up
// subject, subscribe only to its down subject and its private reply prefix.
// No allow_responses: a client can never answer an arbitrary reply subject.
// The deny lists are redundant with the allow lists and kept as a guard
// against a future allow entry that is accidentally too wide.
func ClientPermissions(ingress string) jwt.Permissions {
	var p jwt.Permissions
	p.Pub.Allow.Add(UpSubject(ingress))
	p.Sub.Allow.Add(DownSubject(ingress), ReplyPrefix(ingress)+".>")
	deny := []string{"$SYS.>", "$JS.>", "$KV.>", "$O.>", "_INBOX.>"}
	p.Pub.Deny.Add(deny...)
	p.Sub.Deny.Add(deny...)
	return p
}

// ClientUserClaims builds the (unsigned) User JWT claims for one connection.
func ClientUserClaims(userNkey, account, ingress string, exp time.Time, lim ClientLimits, connTypes []string) *jwt.UserClaims {
	uc := jwt.NewUserClaims(userNkey)
	uc.Audience = account
	// Name replaces the connection's username, so CONNZ shows the ingress as
	// authorized_user; kicks are verified against it.
	uc.Name = ingress
	uc.Expires = exp.Unix()
	uc.Permissions = ClientPermissions(ingress)
	natsLim := &uc.Limits.NatsLimits //nolint:staticcheck // QF1008: UserClaims.Payload() shadows promoted field
	if lim.MaxPayload > 0 {
		natsLim.Payload = lim.MaxPayload
	}
	if lim.MaxSubs > 0 {
		natsLim.Subs = lim.MaxSubs
	}
	for _, t := range connTypes {
		uc.AllowedConnectionTypes.Add(strings.ToUpper(t))
	}
	return uc
}

// clientTypes maps JWT connection types to the auth request client_info.type.
var clientTypes = map[string]string{
	jwt.ConnectionTypeStandard:  "nats",
	jwt.ConnectionTypeWebsocket: "websocket",
}

func transportAllowed(allowed []string, clientType string) bool {
	if len(allowed) == 0 {
		return true
	}
	for _, t := range allowed {
		if ct, ok := clientTypes[strings.ToUpper(t)]; ok && ct == clientType {
			return true
		}
	}
	return false
}
