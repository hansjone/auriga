package network

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"strconv"
	"strings"
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
	Tenant      string
	Realm       string
	Node        string
	NotAfter    time.Time
	CheckAfter  time.Time
	Revoked     bool
	Attempts    int
	Failures    int
	FailedSince time.Time
	RetryAfter  time.Time
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
	var token struct {
		Tenant string `json:"tenant"`
		Node   string `json:"node"`
		Device string `json:"device"`
		Ticket string `json:"ticket"`
	}
	decoder := json.NewDecoder(strings.NewReader(a.Ticket))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&token); err != nil || decoder.Decode(new(any)) != io.EOF || !uuidID.MatchString(token.Tenant) || !uuidID.MatchString(token.Device) || !validNetworkNumber(token.Node) || !ticketID.MatchString(token.Ticket) {
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
	ingress := ingressForTicket(token.Ticket)
	req := &iam.RedeemNetworkTicketRequest{
		Ticket: token.Ticket, Node: token.Node, Device: token.Device,
		ServerAudience: r.cfg.ServerAudience, ServerId: a.ServerID,
		ClientId: strconv.FormatUint(a.ClientID, 10), UserNkey: a.UserNkey,
		Ingress: ingress, ClientAccount: r.cfg.Account,
	}
	ctx, cancel := context.WithTimeout(team.WithActor(ctx, &collab.Actor{TenantId: token.Tenant}), r.cfg.RedeemTimeout)
	defer cancel()
	resp, err := r.rpc.RedeemNetworkTicket(ctx, req)
	if err != nil {
		return Grant{}, err
	}
	if err := ctx.Err(); err != nil {
		return Grant{}, err
	}
	expiry, err := time.Parse(time.RFC3339Nano, resp.GetExpiresAt())
	if err != nil || !expiry.After(time.Now()) || !validNetworkNumber(resp.GetRealm()) || resp.GetNode() != token.Node || resp.GetDevice() != token.Device || resp.GetServerAudience() != r.cfg.ServerAudience || resp.GetIngress() != ingress {
		return Grant{}, errUnauthorized
	}
	// Store without ticket/access token. It is visible to reconciliation before
	// sending the auth response, so successful CONNECT cannot escape tracking.
	r.mu.Lock()
	r.bindings[ingress] = liveBinding{Binding: Binding{ServerID: a.ServerID, ClientID: a.ClientID, Account: r.cfg.Account, Ingress: ingress}, Tenant: token.Tenant, Realm: resp.GetRealm(), Node: token.Node, NotAfter: expiry, CheckAfter: time.Now().Add(r.cfg.AuthTimeout)}
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

type nodeIdentity struct{ Tenant, Realm, Node string }

func (r *IAMRedeemer) activeBatch(ctx context.Context, bindings []liveBinding) (map[nodeIdentity]bool, error) {
	requested := make(map[nodeIdentity]bool, len(bindings))
	req := &iam.BatchNetworkNodesActiveRequest{}
	for _, b := range bindings {
		key := nodeIdentity{b.Tenant, b.Realm, b.Node}
		if _, ok := requested[key]; ok {
			continue
		}
		requested[key] = false
		req.Nodes = append(req.Nodes, &iam.NetworkNodeIdentity{TenantId: b.Tenant, Realm: b.Realm, Node: b.Node})
	}
	if len(req.Nodes) == 0 {
		return requested, nil
	}
	ctx = team.WithActor(ctx, &collab.Actor{TenantId: req.Nodes[0].TenantId})
	resp, err := r.rpc.BatchNetworkNodesActive(ctx, req)
	if err != nil {
		return nil, err
	}
	result := make(map[nodeIdentity]bool, len(requested))
	for _, s := range resp.GetNodes() {
		id := s.GetIdentity()
		key := nodeIdentity{id.GetTenantId(), id.GetRealm(), id.GetNode()}
		_, wanted := requested[key]
		_, duplicate := result[key]
		if !wanted || duplicate {
			return nil, errUnauthorized
		}
		result[key] = s.GetActive()
	}
	if len(result) != len(requested) {
		return nil, errUnauthorized
	}
	return result, ctx.Err()
}
