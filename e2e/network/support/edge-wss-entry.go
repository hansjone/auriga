// This test-only main is overlaid into package main; it never ships in edge.
package main

import (
	"bufio"
	"context"
	"crypto/x509"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hansjone/auriga/infra/gen/deploymentconfig"
)

func edgeWSSRun() error {
	var spec struct{ Upstream, CA, Name, Cert, Key, Access, IAM, HopCA, HopCert, HopKey string }
	raw, err := os.ReadFile(os.Getenv("AURIGA_EDGE_WSS"))
	if err != nil {
		return err
	}
	if err = json.Unmarshal(raw, &spec); err != nil {
		return err
	}
	raw, err = os.ReadFile(spec.Access)
	if err != nil {
		return err
	}
	access, err := deploymentconfig.Load(raw)
	if err != nil {
		return err
	}
	cfg := defaultConfig()
	cfg.Access = access
	cfg.PortalHost = originAuthority(access.PortalOrigin())
	cfg.AdminHost = originAuthority(access.AdminOrigin())
	cfg.TLS = TLSConfig{Mode: "files", CertFile: spec.Cert, KeyFile: spec.Key}
	cfg.Upstreams = map[string]string{"iam": spec.IAM, "collab": spec.IAM}
	cfg.IAMHop = IAMHopConfig{CAFile: spec.HopCA, CertFile: spec.HopCert, KeyFile: spec.HopKey, ServerName: "localhost"}
	cfg.CollabHop = cfg.IAMHop
	cfg.NetworkWSS = NetworkWSSConfig{Enabled: true, UpstreamURL: spec.Upstream, CAFile: spec.CA, ServerName: spec.Name, AllowedOrigins: []string{access.PortalURL()}}
	gw, err := NewGateway(cfg, NewMemorySessionStore(), NewMemoryRouteStore(), NewMemoryTicketStore(), nil, slog.Default())
	if err != nil {
		return err
	}
	defer gw.IAMTransport.(*http.Transport).CloseIdleConnections()
	for _, tr := range gw.APITransports {
		defer tr.(*http.Transport).CloseIdleConnections()
	}
	transport := gw.networkWSS.proxy.Transport.(*http.Transport)
	defer transport.CloseIdleConnections()
	var dials atomic.Int64
	dial := transport.DialContext
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		dials.Add(1)
		return dial(ctx, network, address)
	}
	var mu sync.Mutex
	tlsErrors := []string{}
	original := gw.networkWSS.proxy.ErrorHandler
	gw.networkWSS.proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		kind := "other"
		var unknown x509.UnknownAuthorityError
		var hostname x509.HostnameError
		if errors.As(err, &unknown) {
			kind = "unknown-authority"
		}
		if errors.As(err, &hostname) {
			kind = "hostname"
		}
		mu.Lock()
		tlsErrors = append(tlsErrors, kind)
		mu.Unlock()
		original(w, r, err)
	}
	listeners := map[string]net.Listener{}
	defer func() {
		for _, ln := range listeners {
			_ = ln.Close()
		}
	}()
	entries := access.Listeners()
	for role, port := range map[string]int32{"portal": entries.Portal.Port, "admin": entries.Admin.Port, "instanceControl": entries.InstanceControl.Port} {
		ln, e := net.Listen("tcp", net.JoinHostPort("127.0.0.1", fmtPort(port)))
		if e != nil {
			return e
		}
		listeners[role] = ln
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	server := &Server{Cfg: cfg, Handler: gw.Handler(), Listen: func(_, address string) (net.Listener, error) {
		for _, ln := range listeners {
			if ln.Addr().String() == address {
				return ln, nil
			}
		}
		return nil, errors.New("unowned listener")
	}}
	done := make(chan error, 1)
	go func() { done <- server.Run(ctx) }()
	emit := func(v any) error { return json.NewEncoder(os.Stdout).Encode(v) }
	if err = emit(map[string]any{"stage": "edge-ready", "pid": os.Getpid(), "portal": access.PortalURL(), "admin": access.AdminURL()}); err != nil {
		return err
	}
	input := bufio.NewScanner(os.Stdin)
	for input.Scan() {
		if input.Text() == "stop" {
			break
		}
		var request struct{ ID, Op string }
		if json.Unmarshal(input.Bytes(), &request) != nil || request.Op != "stats" {
			return errors.New("invalid observation")
		}
		mu.Lock()
		observed := append([]string{}, tlsErrors...)
		mu.Unlock()
		if err = emit(map[string]any{"stage": "reply", "id": request.ID, "dials": dials.Load(), "tlsErrors": observed}); err != nil {
			return err
		}
	}
	cancel()
	select {
	case err = <-done:
		return err
	case <-time.After(10 * time.Second):
		return errors.New("edge shutdown deadline")
	}
}
func fmtPort(port int32) string { // no config aliases or synthetic Host headers
	return strconv.Itoa(int(port))
}
