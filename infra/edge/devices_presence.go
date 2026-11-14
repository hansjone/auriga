package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/nats-io/nats.go"
)

// Reuses the network gateway's least-privilege system-account CONNZ/KICK API.
// Credentials must only allow these subjects and private inbox replies.
type DevicePresenceConfig struct {
	msgnet       *edgeMsgnet
	URL          string `json:"url"`
	CredsFile    string `json:"credsFile"`
	Username     string `json:"username"`
	PasswordFile string `json:"passwordFile"`
	CAFile       string `json:"caFile"`
	CertFile     string `json:"certFile"`
	KeyFile      string `json:"keyFile"`
	ServerName   string `json:"serverName"`
}

func (c DevicePresenceConfig) validate() error {
	if c == (DevicePresenceConfig{}) {
		return nil
	}
	u, err := url.Parse(c.URL)
	if err != nil || u.Host == "" || u.User != nil || (u.Scheme != "nats" && u.Scheme != "tls") || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return errDevicePresence
	}
	plain := u.Scheme == "nats" && net.ParseIP(u.Hostname()) != nil && net.ParseIP(u.Hostname()).IsLoopback()
	if !plain && (u.Scheme != "tls" || c.msgnet == nil && (c.CAFile == "" || c.CertFile == "" || c.KeyFile == "")) {
		return errDevicePresence
	}
	if (c.CAFile != "" || c.CertFile != "" || c.KeyFile != "") && (c.CAFile == "" || c.CertFile == "" || c.KeyFile == "") {
		return errDevicePresence
	}
	if c.CredsFile != "" {
		if c.Username != "" || c.PasswordFile != "" {
			return errDevicePresence
		}
		return nil
	}
	if c.Username == "" || c.PasswordFile == "" {
		return errDevicePresence
	}
	return nil
}

type deviceBinding struct {
	Device  string `json:"device"`
	Server  string `json:"serverId"`
	Client  string `json:"clientId"`
	Account string `json:"account"`
	Ingress string `json:"ingress"`
}
type deviceConn struct {
	CID          uint64    `json:"cid"`
	Account      string    `json:"account"`
	User         string    `json:"authorized_user"`
	IP           string    `json:"ip"`
	Port         int       `json:"port"`
	LastActivity time.Time `json:"last_activity"`
}
type deviceState struct {
	Online    string
	Heartbeat time.Time
	Activity  time.Time
}

var deviceServerPattern = regexp.MustCompile(`^N[A-Z2-7]{55}$`)
var deviceIngressPattern = regexp.MustCompile(`^[a-z2-7]{26}$`)
var errDevicePresence = errors.New("device connection state unavailable")
var errDeviceGenerationGone = errors.New("broker generation no longer exists")

type devicePresence struct {
	request    func(context.Context, string, []byte) ([]byte, error)
	heartbeats *deviceHeartbeats
	close      func()
	serverID   string
}

func openDevicePresence(cfg DevicePresenceConfig, heartbeats *deviceHeartbeats) (*devicePresence, error) {
	if cfg.URL == "" || cfg.validate() != nil {
		return nil, errDevicePresence
	}
	opts := []nats.Option{nats.Name("auriga-edge-device-presence"), nats.Timeout(500 * time.Millisecond), nats.NoReconnect()}
	var confirmed func()
	if cfg.msgnet != nil {
		verified, mark, e := cfg.msgnet.presenceOptions(cfg.ServerName)
		if e != nil {
			return nil, errDevicePresence
		}
		opts = append(opts, verified...)
		confirmed = mark
	} else if cfg.CAFile != "" {
		option, err := reloadingPresenceTLS(cfg)
		if err != nil {
			return nil, errDevicePresence
		}
		opts = append(opts, option)
	}
	if cfg.CredsFile != "" {
		opts = append(opts, nats.UserCredentials(cfg.CredsFile))
	} else {
		if cfg.Username == "" || cfg.PasswordFile == "" {
			return nil, errDevicePresence
		}
		secret, err := os.ReadFile(cfg.PasswordFile)
		if err != nil || len(secret) > 16384 || strings.TrimSpace(string(secret)) == "" {
			return nil, errDevicePresence
		}
		opts = append(opts, nats.UserInfo(cfg.Username, strings.TrimSpace(string(secret))))
	}
	nc, err := nats.Connect(cfg.URL, opts...)
	if err != nil {
		return nil, errDevicePresence
	}
	if confirmed != nil {
		confirmed()
	}
	return &devicePresence{request: func(ctx context.Context, subject string, body []byte) ([]byte, error) {
		msg, err := nc.RequestWithContext(ctx, subject, body)
		if err != nil {
			if errors.Is(err, nats.ErrNoResponders) && nc.IsConnected() {
				return nil, nats.ErrNoResponders
			}
			return nil, errDevicePresence
		}
		return msg.Data, nil
	}, heartbeats: heartbeats, close: nc.Close, serverID: nc.ConnectedServerId()}, nil
}
func (p *devicePresence) call(ctx context.Context, server, op string, body any, result any) error {
	if !deviceServerPattern.MatchString(server) {
		return errDevicePresence
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return err
	}
	raw, err := p.request(ctx, "$SYS.REQ.SERVER."+server+"."+op, encoded)
	if err != nil {
		if errors.Is(err, nats.ErrNoResponders) && p.serverID != "" && p.serverID != server {
			return errDeviceGenerationGone
		}
		return err
	}
	var envelope struct {
		Data  json.RawMessage `json:"data"`
		Error json.RawMessage `json:"error"`
	}
	if len(raw) > 8<<20 || json.Unmarshal(raw, &envelope) != nil || (len(envelope.Error) > 0 && string(envelope.Error) != "null") || (result != nil && len(envelope.Data) == 0) {
		return errDevicePresence
	}
	if result != nil {
		return json.Unmarshal(envelope.Data, result)
	}
	return nil
}
func validDeviceBinding(b deviceBinding) bool {
	cid, err := strconv.ParseUint(b.Client, 10, 64)
	return err == nil && cid > 0 && strconv.FormatUint(cid, 10) == b.Client && deviceServerPattern.MatchString(b.Server) && b.Account != "" && deviceIngressPattern.MatchString(b.Ingress) && b.Device != ""
}

func (p *devicePresence) snapshot(ctx context.Context, bindings []deviceBinding) (map[string]deviceState, error) {
	if len(bindings) > 10000 {
		return nil, errDevicePresence
	}
	type snapshot struct {
		Server string       `json:"server_id"`
		Total  int          `json:"total"`
		Conns  []deviceConn `json:"connections"`
	}
	groups := make(map[string]snapshot)
	failed := make(map[string]bool)
	retired := make(map[string]bool)
	ordered := append([]deviceBinding(nil), bindings...)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].Server == p.serverID && ordered[j].Server != p.serverID })
	probes := 0
	for _, b := range ordered {
		if !validDeviceBinding(b) {
			return nil, errDevicePresence
		}
		key := b.Server + "/" + b.Account
		if _, ok := groups[key]; ok || failed[key] {
			continue
		}
		if retired[b.Server] {
			groups[key] = snapshot{Server: b.Server}
			continue
		}
		if probes >= 64 {
			failed[key] = true
			continue
		}
		probes++
		var z snapshot
		queryCtx, cancel := context.WithTimeout(ctx, 250*time.Millisecond)
		err := p.call(queryCtx, b.Server, "CONNZ", map[string]any{"auth": true, "acc": b.Account, "state": 0, "limit": 10001, "offset": 0}, &z)
		cancel()
		if errors.Is(err, errDeviceGenerationGone) {
			retired[b.Server] = true
			groups[key] = snapshot{Server: b.Server}
			continue
		}
		if err != nil {
			failed[key] = true
			continue
		}
		if z.Server != b.Server || z.Total < 0 || z.Total != len(z.Conns) || len(z.Conns) > 10000 {
			return nil, errDevicePresence
		}
		for _, conn := range z.Conns {
			if conn.Account != b.Account {
				return nil, errDevicePresence
			}
		}
		groups[key] = z
	}
	states := make(map[string]deviceState)
	for _, b := range bindings {
		state, ok := states[b.Device]
		if !ok {
			state.Online = "offline"
		}
		if failed[b.Server+"/"+b.Account] && state.Online != "online" {
			state.Online = "unknown"
		}
		cid, _ := strconv.ParseUint(b.Client, 10, 64)
		for _, c := range groups[b.Server+"/"+b.Account].Conns {
			if c.CID != cid {
				continue
			}
			if c.User != b.Ingress {
				return nil, errDevicePresence
			}
			state.Online = "online"
			if c.LastActivity.After(state.Activity) {
				state.Activity = c.LastActivity
			}
			if beat := p.heartbeats.last(c.IP, c.Port); beat.After(state.Heartbeat) {
				state.Heartbeat = beat
			}
		}
		if beat := p.heartbeats.remember(b.Server+"/"+b.Client+"/"+b.Account+"/"+b.Ingress, state.Heartbeat); beat.After(state.Heartbeat) {
			state.Heartbeat = beat
		}
		states[b.Device] = state
	}
	return states, nil
}

func (p *devicePresence) disconnect(ctx context.Context, bindings []deviceBinding) error {
	// Verify the entire ownership snapshot before sending any destructive request.
	states, err := p.snapshot(ctx, bindings)
	if err != nil {
		return err
	}
	for _, state := range states {
		if state.Online == "unknown" {
			return errDevicePresence
		}
	}
	for _, b := range bindings {
		var z struct {
			Server string       `json:"server_id"`
			Conns  []deviceConn `json:"connections"`
		}
		cid, _ := strconv.ParseUint(b.Client, 10, 64)
		if err := p.call(ctx, b.Server, "CONNZ", map[string]any{"cid": cid, "auth": true, "state": 0}, &z); err != nil {
			if errors.Is(err, errDeviceGenerationGone) {
				continue
			}
			return err
		}
		if z.Server != b.Server {
			return errDevicePresence
		}
		found := false
		for _, c := range z.Conns {
			if c.CID == cid {
				if c.Account != b.Account || c.User != b.Ingress {
					return errDevicePresence
				}
				found = true
			}
		}
		if found {
			if err := p.call(ctx, b.Server, "KICK", map[string]uint64{"cid": cid}, nil); err != nil {
				return err
			}
		}
	}
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		states, err := p.snapshot(ctx, bindings)
		if err != nil {
			return err
		}
		live := false
		for _, s := range states {
			if s.Online == "unknown" {
				return errDevicePresence
			}
			live = live || s.Online == "online"
		}
		if !live {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("%w: disconnect not confirmed", errDevicePresence)
		case <-ticker.C:
		}
	}
}
