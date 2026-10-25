package network

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/json"
	"errors"
	"regexp"
	"strconv"
	"sync"
	"time"

	"github.com/hansjone/auriga/infra/ai-gateway/internal/team"
	collab "github.com/hansjone/auriga/infra/gen/auriga/collab/v1"
	"github.com/nats-io/jwt/v2"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"
)

// The descriptor is generated directly from the new contract; no handwritten
// protobuf DTOs or changes to the separately owned infra/gen module.
//
//go:generate protoc --proto_path=../../../../contracts/proto --descriptor_set_out=iam-network.pb auriga/iam/v1/network.proto
//go:embed iam-network.pb
var iamDescriptor []byte

var iamFile = networkDescriptor()

func networkDescriptor() protoreflect.FileDescriptor {
	var set descriptorpb.FileDescriptorSet
	if err := proto.Unmarshal(iamDescriptor, &set); err != nil {
		panic(err)
	}
	file, err := protodesc.NewFile(set.File[0], nil)
	if err != nil {
		panic(err)
	}
	return file
}

const redeemMethod = "/auriga.iam.v1.NetworkGatewayService/RedeemNetworkTicket"
const activeMethod = "/auriga.iam.v1.NetworkGatewayService/NetworkNodeActive"

func setString(m *dynamicpb.Message, field protoreflect.Name, value string) {
	m.Set(m.Descriptor().Fields().ByName(field), protoreflect.ValueOfString(value))
}
func getString(m *dynamicpb.Message, field protoreflect.Name) string {
	return m.Get(m.Descriptor().Fields().ByName(field)).String()
}

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
	rpc      grpc.ClientConnInterface
	cfg      IAMConfig
	mu       sync.Mutex
	pending  int
	bindings map[string]liveBinding
}

func NewIAMRedeemer(rpc grpc.ClientConnInterface, cfg IAMConfig) (*IAMRedeemer, error) {
	if rpc == nil || cfg.ServerID == "" || cfg.ServerAudience == "" || cfg.Account == "" || cfg.RedeemTimeout <= 0 || cfg.RedeemTimeout >= cfg.AuthTimeout || cfg.MaxConnections <= 0 {
		return nil, errors.New("network: incomplete IAM configuration or invalid timeout")
	}
	return &IAMRedeemer{rpc: rpc, cfg: cfg, bindings: make(map[string]liveBinding)}, nil
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
	req := dynamicpb.NewMessage(iamFile.Messages().ByName("RedeemNetworkTicketRequest"))
	for k, v := range map[protoreflect.Name]string{"ticket": token["ticket"], "node": token["node"], "device": token["device"], "server_audience": r.cfg.ServerAudience} {
		setString(req, k, v)
	}
	for k, v := range map[protoreflect.Name]string{"server_id": a.ServerID, "client_id": strconv.FormatUint(a.ClientID, 10), "user_nkey": a.UserNkey, "ingress": ingress, "client_account": r.cfg.Account} {
		setString(req, k, v)
	}
	resp := dynamicpb.NewMessage(iamFile.Messages().ByName("RedeemNetworkTicketResponse"))
	ctx, cancel := context.WithTimeout(team.WithActor(ctx, &collab.Actor{TenantId: token["tenant"]}), r.cfg.RedeemTimeout)
	defer cancel()
	if err := r.rpc.Invoke(ctx, redeemMethod, req, resp); err != nil {
		return Grant{}, err
	}
	if err := ctx.Err(); err != nil {
		return Grant{}, err
	}
	expiry, err := time.Parse(time.RFC3339Nano, getString(resp, "expires_at"))
	if err != nil || !expiry.After(time.Now()) || !validNetworkNumber(getString(resp, "realm")) || getString(resp, "node") != token["node"] || getString(resp, "device") != token["device"] || getString(resp, "server_audience") != r.cfg.ServerAudience || getString(resp, "ingress") != ingress {
		return Grant{}, errUnauthorized
	}
	// Store without ticket/access token. It is visible to reconciliation before
	// sending the auth response, so successful CONNECT cannot escape tracking.
	r.mu.Lock()
	r.bindings[ingress] = liveBinding{Binding: Binding{ServerID: a.ServerID, ClientID: a.ClientID, Account: r.cfg.Account, Ingress: ingress}, Tenant: token["tenant"], Realm: getString(resp, "realm"), Node: token["node"], NotAfter: expiry, CheckAfter: time.Now().Add(r.cfg.AuthTimeout)}
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
	req := dynamicpb.NewMessage(iamFile.Messages().ByName("NetworkNodeActiveRequest"))
	setString(req, "realm", b.Realm)
	setString(req, "node", b.Node)
	resp := dynamicpb.NewMessage(iamFile.Messages().ByName("NetworkNodeActiveResponse"))
	ctx = team.WithActor(ctx, &collab.Actor{TenantId: b.Tenant})
	if err := r.rpc.Invoke(ctx, activeMethod, req, resp); err != nil {
		return false, err
	}
	return resp.Get(resp.Descriptor().Fields().ByName("active")).Bool(), ctx.Err()
}
