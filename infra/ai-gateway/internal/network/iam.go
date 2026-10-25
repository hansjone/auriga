package network

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"regexp"
	"strconv"
	"sync"
	"time"

	"github.com/hansjone/auriga/infra/ai-gateway/internal/team"
	collab "github.com/hansjone/auriga/infra/gen/auriga/collab/v1"
	iam "github.com/hansjone/auriga/infra/gen/auriga/iam/v1"
	"github.com/nats-io/jwt/v2"
	"google.golang.org/grpc"
)

var uuidID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
var ticketID = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)

func validNetworkNumber(value string) bool {
	n, err := strconv.ParseUint(value, 10, 64)
	return err == nil && n != 0 && strconv.FormatUint(n, 10) == value
}

// The ticket is server-generated random material. Derivation lets the preparation
// side tell the client its private subjects before CONNECT; it conveys no ACL.
func ingressForTicket(ticket string) string {
	digest := sha256.Sum256([]byte(ticket))
	return ingressEncoding.EncodeToString(digest[:ingressBytes])
}

type IAMConfig struct {
	ServerID       string
	ServerAudience string
	Account        string
	RedeemTimeout  time.Duration
	AuthTimeout    time.Duration
	MaxConnections int
}

type liveBinding struct {
	Binding
	Tenant     string
	Realm      string
	Node       string
	NotAfter   time.Time
	CheckAfter time.Time
	Revoked    bool
	Attempts   int
}

// IAMRedeemer retains only bounded live connections. Routing hints in CONNECT
// are never authority; the response identity is bound to a fresh private ingress.
type IAMRedeemer struct {
	rpc      iam.NetworkGatewayServiceClient
	cfg      IAMConfig
	mu       sync.Mutex
	pending  int
	bindings map[string]liveBinding
}

func NewIAMRedeemer(rpc grpc.ClientConnInterface, cfg IAMConfig) (*IAMRedeemer, error) {
	if rpc == nil || cfg.ServerID == "" || cfg.ServerAudience == "" || cfg.Account == "" || cfg.RedeemTimeout <= 0 || cfg.RedeemTimeout >= cfg.AuthTimeout || cfg.MaxConnections <= 0 {
		return nil, errors.New("network: incomplete IAM configuration or invalid timeout")
	}
	return &IAMRedeemer{rpc: iam.NewNetworkGatewayServiceClient(rpc), cfg: cfg, bindings: make(map[string]liveBinding)}, nil
}

func (r *IAMRedeemer) Redeem(ctx context.Context, a Attempt) (Grant, error) {
	if a.ServerID != r.cfg.ServerID || a.ClientID == 0 || a.UserNkey == "" || len(a.Ticket) > 2048 {
		return Grant{}, errUnauthorized
	}
	var token map[string]string
	if err := json.Unmarshal([]byte(a.Ticket), &token); err != nil || !uuidID.MatchString(token["tenant"]) || !uuidID.MatchString(token["device"]) || !validNetworkNumber(token["node"]) || !ticketID.MatchString(token["ticket"]) {
		return Grant{}, errUnauthorized
	}
	r.mu.Lock()
	if len(r.bindings)+r.pending >= r.cfg.MaxConnections {
		r.mu.Unlock()
		return Grant{}, errors.New("network: connection capacity reached")
	}
	r.pending++
	r.mu.Unlock()
	defer func() { r.mu.Lock(); r.pending--; r.mu.Unlock() }()
	ingress := ingressForTicket(token["ticket"])
	req := &iam.RedeemNetworkTicketRequest{
		Ticket: token["ticket"], Node: token["node"], Device: token["device"],
		ServerAudience: r.cfg.ServerAudience, ServerId: a.ServerID,
		ClientId: strconv.FormatUint(a.ClientID, 10), UserNkey: a.UserNkey,
		Ingress: ingress, ClientAccount: r.cfg.Account,
	}
	ctx, cancel := context.WithTimeout(team.WithActor(ctx, &collab.Actor{TenantId: token["tenant"]}), r.cfg.RedeemTimeout)
	defer cancel()
	resp, err := r.rpc.RedeemNetworkTicket(ctx, req)
	if err != nil {
		return Grant{}, err
	}
	if err := ctx.Err(); err != nil {
		return Grant{}, err
	}
	expiry, err := time.Parse(time.RFC3339Nano, resp.GetExpiresAt())
	if err != nil || !expiry.After(time.Now()) || !validNetworkNumber(resp.GetRealm()) || resp.GetNode() != token["node"] || resp.GetDevice() != token["device"] || resp.GetServerAudience() != r.cfg.ServerAudience || resp.GetIngress() != ingress {
		return Grant{}, errUnauthorized
	}
	// Store without ticket/access token. It is visible to reconciliation before
	// sending the auth response, so successful CONNECT cannot escape tracking.
	r.mu.Lock()
	r.bindings[ingress] = liveBinding{Binding: Binding{ServerID: a.ServerID, ClientID: a.ClientID, Account: r.cfg.Account, Ingress: ingress}, Tenant: token["tenant"], Realm: resp.GetRealm(), Node: token["node"], NotAfter: expiry, CheckAfter: time.Now().Add(r.cfg.AuthTimeout)}
	r.mu.Unlock()
	return Grant{Ingress: ingress, NotAfter: expiry}, nil
}

func (r *IAMRedeemer) snapshot() []liveBinding {
	r.mu.Lock()
	defer r.mu.Unlock()
	result := make([]liveBinding, 0, len(r.bindings))
	for _, b := range r.bindings {
		result = append(result, b)
	}
	return result
}

func (r *IAMRedeemer) onDecision(a Attempt, claims *jwt.UserClaims, decisionErr error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for ingress, b := range r.bindings {
		if b.ServerID != a.ServerID || b.ClientID != a.ClientID {
			continue
		}
		if decisionErr != nil || claims == nil {
			delete(r.bindings, ingress)
		} else {
			b.NotAfter = time.Unix(claims.Expires, 0)
			r.bindings[ingress] = b
		}
	}
}

func (r *IAMRedeemer) active(ctx context.Context, b liveBinding) (bool, error) {
	req := &iam.NetworkNodeActiveRequest{Realm: b.Realm, Node: b.Node}
	ctx = team.WithActor(ctx, &collab.Actor{TenantId: b.Tenant})
	resp, err := r.rpc.NetworkNodeActive(ctx, req)
	if err != nil {
		return false, err
	}
	return resp.GetActive(), ctx.Err()
}
