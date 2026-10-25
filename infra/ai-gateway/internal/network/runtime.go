package network

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/url"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/hansjone/auriga/infra/ai-gateway/internal/team"
	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nats-server/v2/conf"
	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nkeys"
	"google.golang.org/grpc"
)

type NATSConfig struct {
	URL          string `json:"url"`
	Username     string `json:"username"`
	PasswordFile string `json:"passwordFile"`
	CertFile     string `json:"certFile"`
	KeyFile      string `json:"keyFile"`
	CAFile       string `json:"caFile"`
}

// RuntimeConfig references the SAME config file used to launch nats-server.
// authorization.timeout, issuer and XKey are read/verified there, not duplicated.
type RuntimeConfig struct {
	BrokerConfigFile     string         `json:"brokerConfigFile"`
	ServerAudience       string         `json:"serverAudience"`
	ClientAccount        string         `json:"clientAccount"`
	IssuerSeedFile       string         `json:"issuerSeedFile"`
	XKeySeedFile         string         `json:"xkeySeedFile"`
	IAM                  team.RPCConfig `json:"iam"`
	Auth                 NATSConfig     `json:"auth"`
	Ops                  NATSConfig     `json:"ops"`
	RedeemTimeoutMillis  int            `json:"redeemTimeoutMillis"`
	ActiveIntervalMillis int            `json:"activeIntervalMillis"`
	RequestTimeoutMillis int            `json:"requestTimeoutMillis"`
	MaxKickAttempts      int            `json:"maxKickAttempts"`
	MaxConnections       int            `json:"maxConnections"`
}

func LoadRuntimeConfig(path string) (RuntimeConfig, error) {
	var cfg RuntimeConfig
	file, err := os.Open(path)
	if err != nil {
		return cfg, err
	}
	defer func() { _ = file.Close() }()
	decoder := json.NewDecoder(io.LimitReader(file, 65537))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&cfg); err != nil {
		return cfg, errors.New("network: invalid runtime configuration")
	}
	if decoder.Decode(new(any)) != io.EOF {
		return cfg, errors.New("network: trailing runtime configuration")
	}
	return cfg, nil
}

type Runtime struct {
	auth     *nats.Conn
	ops      *nats.Conn
	iam      *grpc.ClientConn
	issuer   nkeys.KeyPair
	xkey     nkeys.KeyPair
	redeemer *IAMRedeemer
	cancel   context.CancelFunc
	done     sync.WaitGroup
	once     sync.Once
	Errors   chan error
}

func (r *Runtime) Close() {
	r.once.Do(func() {
		if r.cancel != nil {
			r.cancel()
		}
		r.done.Wait()
		if r.auth != nil {
			r.auth.Close()
		}
		if r.ops != nil {
			r.ops.Close()
		}
		if r.iam != nil {
			_ = r.iam.Close()
		}
		if r.issuer != nil {
			r.issuer.Wipe()
		}
		if r.xkey != nil {
			r.xkey.Wipe()
		}
	})
}

func RunRuntime(ctx context.Context, cfg RuntimeConfig) error {
	runtime, err := StartRuntime(ctx, cfg)
	if err != nil {
		return err
	}
	defer runtime.Close()
	select {
	case <-ctx.Done():
		return nil
	case err = <-runtime.Errors:
		return err
	}
}

func seedFile(path string) (nkeys.KeyPair, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, errors.New("network: required key file unavailable")
	}
	defer clear(raw)
	kp, err := nkeys.FromSeed([]byte(strings.TrimSpace(string(raw))))
	if err != nil {
		return nil, errors.New("network: invalid key file")
	}
	return kp, nil
}

func brokerClientLimits(path, account string) (ClientLimits, error) {
	config, err := conf.ParseFile(path)
	if err != nil {
		return ClientLimits{}, errors.New("network: invalid Broker limits configuration")
	}
	accounts, _ := config["accounts"].(map[string]any)
	client, _ := accounts[account].(map[string]any)
	limits, _ := client["limits"].(map[string]any)
	payload, _ := limits["max_payload"].(int64)
	subscriptions, _ := limits["max_subscriptions"].(int64)
	if payload <= 0 || payload > EncodingMaxPayloadBytes || subscriptions <= 0 {
		return ClientLimits{}, errors.New("network: client account requires bounded payload and subscription limits")
	}
	return ClientLimits{MaxPayload: payload, MaxSubs: subscriptions}, nil
}

func dialNATS(cfg NATSConfig, closed func(*nats.Conn)) (*nats.Conn, error) {
	u, err := url.Parse(cfg.URL)
	if err != nil || u.User != nil || u.Host == "" || cfg.Username == "" || cfg.PasswordFile == "" {
		return nil, errors.New("network: missing Broker endpoint or identity")
	}
	ip := net.ParseIP(u.Hostname())
	plain := u.Scheme == "nats" && ip != nil && ip.IsLoopback()
	if !plain && (u.Scheme != "tls" || cfg.CertFile == "" || cfg.KeyFile == "" || cfg.CAFile == "") {
		return nil, errors.New("network: non-loopback Broker requires mTLS")
	}
	password, err := os.ReadFile(cfg.PasswordFile)
	if err != nil || strings.TrimSpace(string(password)) == "" {
		return nil, errors.New("network: required Broker identity file unavailable")
	}
	defer clear(password)
	options := []nats.Option{nats.UserInfo(cfg.Username, strings.TrimSpace(string(password))), nats.NoReconnect(), nats.Timeout(2 * time.Second), nats.ClosedHandler(closed)}
	if !plain || cfg.CertFile != "" || cfg.KeyFile != "" || cfg.CAFile != "" {
		options = append(options, nats.RootCAs(cfg.CAFile), nats.ClientCert(cfg.CertFile, cfg.KeyFile))
	}
	nc, err := nats.Connect(cfg.URL, options...)
	if err != nil {
		return nil, errors.New("network: Broker connection failed")
	}
	return nc, nil
}

func StartRuntime(parent context.Context, cfg RuntimeConfig) (runtime *Runtime, err error) {
	if cfg.BrokerConfigFile == "" || cfg.ServerAudience == "" || cfg.ClientAccount == "" || cfg.IssuerSeedFile == "" || cfg.XKeySeedFile == "" || cfg.IAM.Target == "" || cfg.IAM.ServiceTokenFile == "" || cfg.RedeemTimeoutMillis <= 0 || cfg.RedeemTimeoutMillis > 10000 || cfg.ActiveIntervalMillis <= 0 || cfg.ActiveIntervalMillis > 1000 || cfg.RequestTimeoutMillis <= 0 || cfg.RequestTimeoutMillis > 1000 || cfg.MaxConnections <= 0 || cfg.MaxConnections > 10000 || cfg.MaxKickAttempts < 1 || cfg.MaxKickAttempts > 5 {
		return nil, errors.New("network: incomplete runtime configuration")
	}
	opts, e := server.ProcessConfigFile(cfg.BrokerConfigFile)
	if e != nil {
		return nil, errors.New("network: invalid Broker configuration")
	}
	if opts.AuthCallout == nil || opts.AuthTimeout <= 0 || opts.ServerName == "" || opts.SystemAccount == "" || cfg.ClientAccount == opts.SystemAccount || cfg.ClientAccount == opts.AuthCallout.Account || opts.Websocket.AuthTimeout != 0 && opts.Websocket.AuthTimeout != opts.AuthTimeout {
		return nil, errors.New("network: Broker needs named server, callout, accounts and shared authorization.timeout")
	}
	authTimeout := time.Duration(opts.AuthTimeout * float64(time.Second))
	redeemTimeout := time.Duration(cfg.RedeemTimeoutMillis) * time.Millisecond
	if redeemTimeout >= authTimeout {
		return nil, errors.New("network: redeem timeout must be less than authorization.timeout")
	}
	limits, e := brokerClientLimits(cfg.BrokerConfigFile, cfg.ClientAccount)
	if e != nil {
		return nil, e
	}
	authIdentity, opsIdentity, clientAccount := false, false, false
	for _, u := range opts.Users {
		if u.Username == cfg.Auth.Username && u.Account != nil && u.Account.Name == opts.AuthCallout.Account {
			authIdentity = true
		}
		if u.Username == cfg.Ops.Username && u.Account != nil && u.Account.Name == opts.SystemAccount {
			opsIdentity = true
		}
	}
	for _, account := range opts.Accounts {
		if account.Name == cfg.ClientAccount {
			clientAccount = true
		}
	}
	if !authIdentity || !opsIdentity || !clientAccount || cfg.Auth.Username == cfg.Ops.Username || !slices.Contains(opts.AuthCallout.AuthUsers, cfg.Auth.Username) || !slices.Contains(opts.AuthCallout.AuthUsers, cfg.Ops.Username) {
		return nil, errors.New("network: Broker auth/system identities or client account mismatch")
	}
	token, e := os.ReadFile(cfg.IAM.ServiceTokenFile)
	if e != nil || strings.TrimSpace(string(token)) == "" {
		return nil, errors.New("network: internal service JWT file unavailable")
	}
	clear(token)
	r := &Runtime{Errors: make(chan error, 2)}
	defer func() {
		if err != nil {
			r.Close()
		}
	}()
	if r.issuer, err = seedFile(cfg.IssuerSeedFile); err != nil {
		return nil, err
	}
	if r.xkey, err = seedFile(cfg.XKeySeedFile); err != nil {
		return nil, err
	}
	issuerPub, e := r.issuer.PublicKey()
	if e != nil || issuerPub != opts.AuthCallout.Issuer {
		return nil, errors.New("network: callout issuer mismatch")
	}
	xPub, e := r.xkey.PublicKey()
	if e != nil || !nkeys.IsValidPublicCurveKey(xPub) || xPub != opts.AuthCallout.XKey {
		return nil, errors.New("network: callout encryption key mismatch")
	}
	closed := func(*nats.Conn) {
		select {
		case r.Errors <- errors.New("network: Broker control connection closed; restart required"):
		default:
		}
	}
	if r.auth, err = dialNATS(cfg.Auth, closed); err != nil {
		return nil, err
	}
	if r.ops, err = dialNATS(cfg.Ops, closed); err != nil {
		return nil, err
	}
	if r.auth.ConnectedServerName() != opts.ServerName || r.ops.ConnectedServerId() != r.auth.ConnectedServerId() {
		return nil, errors.New("network: Broker identity/configuration mismatch")
	}
	if r.iam, err = team.DialRPC(cfg.IAM); err != nil {
		return nil, err
	}
	r.redeemer, err = NewIAMRedeemer(r.iam, IAMConfig{ServerID: r.auth.ConnectedServerId(), ServerAudience: cfg.ServerAudience, Account: cfg.ClientAccount, RedeemTimeout: redeemTimeout, AuthTimeout: authTimeout, MaxConnections: cfg.MaxConnections})
	if err != nil {
		return nil, err
	}
	callout, e := NewCallout(CalloutConfig{Issuer: r.issuer, XKey: r.xkey, Account: cfg.ClientAccount, Redeemer: r.redeemer, RedeemTimeout: redeemTimeout, AuthTimeout: authTimeout, Limits: limits, ConnectionTypes: []string{jwt.ConnectionTypeWebsocket}, OnDecision: r.redeemer.onDecision})
	if e != nil {
		return nil, e
	}
	if _, err = callout.Serve(r.auth); err != nil {
		return nil, err
	}
	if err = r.auth.FlushTimeout(authTimeout); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(parent)
	r.cancel = cancel
	r.done.Go(func() {
		_ = r.redeemer.Reconcile(ctx, ReconcileConfig{Interval: time.Duration(cfg.ActiveIntervalMillis) * time.Millisecond, RequestTimeout: time.Duration(cfg.RequestTimeoutMillis) * time.Millisecond, MaxKickAttempts: cfg.MaxKickAttempts, Inspect: func(ctx context.Context, b Binding) error { return CheckBinding(ctx, r.ops, b) }, OnError: func(b Binding, err error) {
			slog.Error("network connection KICK exhausted", "server", b.ServerID, "cid", b.ClientID, "error", err)
		}}, func(ctx context.Context, b Binding) error { return KickBinding(ctx, r.ops, b) })
	})
	return r, nil
}
