package network

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/nats-io/nats.go"
)

// System-account request subjects, nats-server v2.12.6 server/events.go:62-63
// and the generic server-directed form ($SYS.REQ.SERVER.%s.%s).
func kickSubject(serverID string) string  { return "$SYS.REQ.SERVER." + serverID + ".KICK" }
func ldmSubject(serverID string) string   { return "$SYS.REQ.SERVER." + serverID + ".LDM" }
func connzSubject(serverID string) string { return "$SYS.REQ.SERVER." + serverID + ".CONNZ" }

// SysPublishAllow lists the only subjects an Auriga ops identity in the system
// account needs. External DSH connections never get any $SYS permission.
var SysPublishAllow = []string{
	"$SYS.REQ.SERVER.*.KICK",
	"$SYS.REQ.SERVER.*.LDM",
	"$SYS.REQ.SERVER.*.CONNZ",
}

// Binding is the authoritative record written atomically with ticket
// redemption: which server-local connection carries which ingress.
type Binding struct {
	ServerID string
	ClientID uint64
	Account  string
	Ingress  string
}

var (
	ErrNotOwned   = errors.New("network: connection not owned by binding")
	ErrNoSuchConn = errors.New("network: no such connection on server")
	ErrSysAPI     = errors.New("network: system API error")
)

type apiError struct {
	Code        int    `json:"code"`
	Description string `json:"description,omitempty"`
}

type serverAPIResponse struct {
	Data  json.RawMessage `json:"data,omitempty"`
	Error *apiError       `json:"error,omitempty"`
}

// ConnInfo is the subset of CONNZ used to verify ownership.
type ConnInfo struct {
	CID            uint64   `json:"cid"`
	Kind           string   `json:"kind,omitempty"`
	Type           string   `json:"type,omitempty"`
	AuthorizedUser string   `json:"authorized_user,omitempty"`
	Account        string   `json:"account,omitempty"`
	Subs           []string `json:"subscriptions_list,omitempty"`
}

type connz struct {
	ID    string      `json:"server_id"`
	Conns []*ConnInfo `json:"connections"`
	Total int         `json:"total"`
}

// LookupConns gets one complete account snapshot. A truncated response is an
// error, never proof that omitted live connections have disappeared.
func LookupConns(ctx context.Context, nc *nats.Conn, serverID, account string, limit int) (map[uint64]*ConnInfo, error) {
	data, err := sysRequest(ctx, nc, connzSubject(serverID), map[string]any{"auth": true, "acc": account, "state": 0, "limit": limit + 1, "offset": 0})
	if err != nil {
		return nil, err
	}
	var z connz
	if err = json.Unmarshal(data, &z); err != nil {
		return nil, err
	}
	if z.ID != serverID || z.Total > len(z.Conns) || len(z.Conns) > limit {
		return nil, ErrSysAPI
	}
	result := make(map[uint64]*ConnInfo, len(z.Conns))
	for _, ci := range z.Conns {
		if ci.Account != account {
			return nil, ErrNotOwned
		}
		result[ci.CID] = ci
	}
	return result, nil
}

func sysRequest(ctx context.Context, nc *nats.Conn, subj string, body any) (json.RawMessage, error) {
	b, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	msg, err := nc.RequestWithContext(ctx, subj, b)
	if err != nil {
		return nil, err
	}
	var r serverAPIResponse
	if err := json.Unmarshal(msg.Data, &r); err != nil {
		return nil, err
	}
	if r.Error != nil {
		return nil, fmt.Errorf("%w: %d %s", ErrSysAPI, r.Error.Code, r.Error.Description)
	}
	return r.Data, nil
}

// LookupConn queries one open connection by CID on one server, including
// its subscriptions.
func LookupConn(ctx context.Context, nc *nats.Conn, serverID string, cid uint64) (*ConnInfo, error) {
	data, err := sysRequest(ctx, nc, connzSubject(serverID), map[string]any{
		"cid": cid, "auth": true, "subscriptions": true, "state": 0,
	})
	if err != nil {
		return nil, err
	}
	var z connz
	if err := json.Unmarshal(data, &z); err != nil {
		return nil, err
	}
	if z.ID != serverID {
		return nil, fmt.Errorf("network: connz answered by %q, want %q", z.ID, serverID)
	}
	for _, c := range z.Conns {
		if c.CID == cid {
			return c, nil
		}
	}
	return nil, ErrNoSuchConn
}

// Kick closes the connection (server_id, cid). It is addressed to one server
// instance: after a restart the server has a new ID, so an old request finds
// no responder instead of hitting a reused CID.
func Kick(ctx context.Context, nc *nats.Conn, serverID string, cid uint64) error {
	_, err := sysRequest(ctx, nc, kickSubject(serverID), map[string]uint64{"cid": cid})
	return err
}

// LameDuck sends one connection an INFO with ldm=true. It is a migration
// hint only; it neither revokes authorization nor closes the connection.
func LameDuck(ctx context.Context, nc *nats.Conn, serverID string, cid uint64) error {
	_, err := sysRequest(ctx, nc, ldmSubject(serverID), map[string]uint64{"cid": cid})
	return err
}

// KickBinding verifies via CONNZ that (server_id, cid) still carries the
// bound ingress in the bound account, then kicks it. CIDs are never reused
// within one server instance, so the check cannot race with reuse there.
func KickBinding(ctx context.Context, nc *nats.Conn, b Binding) error {
	if err := CheckBinding(ctx, nc, b); err != nil {
		return err
	}
	return Kick(ctx, nc, b.ServerID, b.ClientID)
}

// CheckBinding checks the current server/account/ingress without changing it.
func CheckBinding(ctx context.Context, nc *nats.Conn, b Binding) error {
	ci, err := LookupConn(ctx, nc, b.ServerID, b.ClientID)
	if err != nil {
		return err
	}
	if ci.AuthorizedUser != b.Ingress || ci.Account != b.Account {
		return ErrNotOwned
	}
	return nil
}
