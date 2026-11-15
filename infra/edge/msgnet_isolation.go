package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"time"

	"github.com/hansjone/auriga/infra/edge/internal/projection"
	trust "github.com/hansjone/auriga/infra/edge/pkg/msgnet"
	api "github.com/hansjone/auriga/infra/gen/msgnetapi"
)

// Dedicated private listener. Only the pinned IAM execution identity may call
// after committing its deny fence. It is never registered on Gateway.Handler.
type MsgnetIsolationConfig struct {
	Listen      string `json:"listen"`
	CertFile    string `json:"certFile"`
	KeyFile     string `json:"keyFile"`
	CAFile      string `json:"caFile"`
	CallerSPKI  string `json:"callerSpki"`
	TargetsFile string `json:"targetsFile"`
	SchemaFile  string `json:"schemaFile"`
}

var msgnetDeviceID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
var msgnetTargetID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:@-]{0,127}$`)

type msgnetIsolationTarget struct {
	Tenant  string   `json:"tenant_id"`
	Realm   string   `json:"realm"`
	Target  string   `json:"target_id"`
	Devices []string `json:"devices"`
}

type msgnetIsolation struct {
	gateway *Gateway
	schemas *trust.Schemas
	targets map[string]map[string]bool
	caller  string
	slots   chan struct{}
}

func newMsgnetIsolation(g *Gateway, cfg MsgnetIsolationConfig) (*msgnetIsolation, *tls.Config, error) {
	if runtime.GOOS != "linux" || cfg.Listen == "" || g.DevicesRPC == nil || !g.Cfg.Access.Valid() {
		return nil, nil, fmt.Errorf("private Linux isolation listener requires IAM bindings and installation access")
	}
	caller, e := base64.RawURLEncoding.Strict().DecodeString(cfg.CallerSPKI)
	if e != nil {
		return nil, nil, e
	}
	if _, e = x509.ParsePKIXPublicKey(caller); e != nil {
		return nil, nil, e
	}
	key, e := projection.ReadPrivateConfiguration(cfg.KeyFile, 16384)
	if e != nil {
		return nil, nil, e
	}
	defer clear(key)
	cert, e := os.ReadFile(cfg.CertFile)
	if e != nil {
		return nil, nil, e
	}
	identity, e := tls.X509KeyPair(cert, key)
	if e != nil {
		return nil, nil, e
	}
	ca, e := os.ReadFile(cfg.CAFile)
	if e != nil {
		return nil, nil, e
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(ca) {
		return nil, nil, fmt.Errorf("empty isolation client CA")
	}
	raw, e := projection.ReadPrivateConfiguration(cfg.TargetsFile, 1<<20)
	if e != nil {
		return nil, nil, e
	}
	if _, e = trust.Canonical(raw); e != nil {
		return nil, nil, e
	}
	var bindings []msgnetIsolationTarget
	if e = trust.Decode(raw, &bindings, false); e != nil {
		return nil, nil, e
	}
	schemas, e := trust.LoadSchemas(cfg.SchemaFile)
	if e != nil {
		return nil, nil, e
	}
	x := &msgnetIsolation{gateway: g, schemas: schemas, caller: cfg.CallerSPKI, slots: make(chan struct{}, 4), targets: map[string]map[string]bool{}}
	for _, b := range bindings {
		realm, realmError := strconv.ParseUint(b.Realm, 10, 64)
		if realmError != nil || realm == 0 || strconv.FormatUint(realm, 10) != b.Realm || !msgnetTargetID.MatchString(b.Target) {
			return nil, nil, fmt.Errorf("invalid isolation scope")
		}
		key := b.Realm + "/" + b.Target
		if b.Tenant != g.Cfg.Access.TenantID() || b.Realm == "" || b.Target == "" || len(b.Devices) > 10000 || x.targets[key] != nil {
			return nil, nil, fmt.Errorf("invalid trusted isolation target")
		}
		devices := map[string]bool{}
		for _, id := range b.Devices {
			if !msgnetDeviceID.MatchString(id) || devices[id] {
				return nil, nil, fmt.Errorf("invalid isolation device")
			}
			devices[id] = true
		}
		x.targets[key] = devices
	}
	return x, &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{identity}, ClientCAs: roots, ClientAuth: tls.RequireAndVerifyClientCert}, nil
}

func (x *msgnetIsolation) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.URL.Path != "/api/collab/v2/network/isolation" || r.URL.RawQuery != "" || r.Method != http.MethodPost {
		http.Error(w, "not found", 404)
		return
	}
	if r.TLS == nil || len(r.TLS.VerifiedChains) == 0 || len(r.TLS.PeerCertificates) == 0 || base64.RawURLEncoding.EncodeToString(r.TLS.PeerCertificates[0].RawSubjectPublicKeyInfo) != x.caller {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	select {
	case x.slots <- struct{}{}:
		defer func() { <-x.slots }()
	default:
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	raw, e := io.ReadAll(io.LimitReader(r.Body, 65537))
	var record api.IsolationRecord
	if e != nil || len(raw) > 65536 || x.schemas.Decode("IsolationRecord", raw, &record) != nil || record.TenantId != x.gateway.Cfg.Access.TenantID() || record.State != "quarantined" || record.NewChainReady {
		http.Error(w, "invalid isolation", 400)
		return
	}
	devices := x.targets[record.Realm+"/"+record.TargetId]
	if devices == nil {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	bindings, e := x.gateway.DevicesRPC.ReadDeviceBindings(ctx, record.TenantId)
	if e != nil {
		http.Error(w, "bindings unavailable", http.StatusServiceUnavailable)
		return
	}
	selected := make([]deviceBinding, 0)
	for _, b := range bindings {
		if devices[b.Device] {
			selected = append(selected, b)
		}
	}
	if len(selected) > 0 {
		presence, e := openDevicePresence(x.gateway.Cfg.DevicePresence, nil)
		if e != nil {
			http.Error(w, "presence unavailable", http.StatusServiceUnavailable)
			return
		}
		defer presence.close()
		if e = presence.disconnect(ctx, selected); e != nil {
			http.Error(w, "disconnect unconfirmed", http.StatusServiceUnavailable)
			return
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(record)
}

func (g *Gateway) startMsgnetIsolation(ctx context.Context, cfg *MsgnetIsolationConfig) (func(), error) {
	if cfg == nil {
		return func() {}, nil
	}
	x, tlsConfig, e := newMsgnetIsolation(g, *cfg)
	if e != nil {
		return nil, e
	}
	if !filepath.IsAbs(cfg.TargetsFile) {
		return nil, fmt.Errorf("absolute isolation targets required")
	}
	listener, e := net.Listen("tcp", cfg.Listen)
	if e != nil {
		return nil, e
	}
	srv := &http.Server{Handler: x, TLSConfig: tlsConfig, ReadHeaderTimeout: 3 * time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 7 * time.Second, IdleTimeout: 10 * time.Second, MaxHeaderBytes: 8192, BaseContext: func(net.Listener) context.Context { return ctx }}
	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := srv.Serve(tls.NewListener(listener, tlsConfig)); err != nil && !errors.Is(err, http.ErrServerClosed) {
			g.Log.Error("private message isolation listener stopped", "error", err)
		}
	}()
	return func() { _ = srv.Close(); <-done }, nil
}
