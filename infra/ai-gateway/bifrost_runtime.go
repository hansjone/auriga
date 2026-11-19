package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/hansjone/auriga/infra/ai-gateway/internal/bifrost"
	"github.com/hansjone/auriga/infra/ai-gateway/internal/modelgate"
	"github.com/hansjone/auriga/infra/ai-gateway/internal/team"
	iam "github.com/hansjone/auriga/infra/gen/auriga/iam/v1"
	org "github.com/hansjone/auriga/infra/gen/auriga/org/v1"
)

type bifrostConfig struct {
	Listen             string               `json:"listen"`
	TLSCertFile        string               `json:"tlsCertFile"`
	TLSKeyFile         string               `json:"tlsKeyFile"`
	Origin             string               `json:"origin"`
	MemberPasswordFile string               `json:"memberPasswordFile"`
	MemberPolicy       bifrost.MemberPolicy `json:"memberPolicy"`
	IAM                team.RPCConfig       `json:"iam"`
	Org                team.RPCConfig       `json:"org"`
	SharedModel        *sharedModelConfig   `json:"sharedModel,omitempty"`
	SharedMessages     *sharedMessageConfig `json:"sharedMessages,omitempty"`
}

func serveBifrost(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	var c bifrostConfig
	d := json.NewDecoder(io.LimitReader(f, 1<<20))
	d.DisallowUnknownFields()
	if d.Decode(&c) != nil || d.Decode(new(any)) != io.EOF {
		return errors.New("invalid Bifrost runtime configuration")
	}
	host, _, err := net.SplitHostPort(c.Listen)
	if err != nil {
		return errors.New("invalid Bifrost listen address")
	}
	if c.TLSCertFile == "" || c.TLSKeyFile == "" {
		ip := net.ParseIP(host)
		if ip == nil || !ip.IsLoopback() {
			return errors.New("relay member listener requires TLS")
		}
	}
	ic, err := team.DialRPC(c.IAM)
	if err != nil {
		return err
	}
	defer func() { _ = ic.Close() }()
	oc, err := team.DialRPC(c.Org)
	if err != nil {
		return err
	}
	defer func() { _ = oc.Close() }()
	authority, err := modelgate.NewAuthority(iam.NewDeviceAccessServiceClient(ic), org.NewMemberServiceClient(oc), org.NewAuthorizationServiceClient(oc), time.Now)
	if err != nil {
		return err
	}
	handler, err := bifrost.New(c.Origin, c.MemberPasswordFile, c.MemberPolicy, func(ctx context.Context, raw string) (bifrost.Principal, error) {
		id, e := authority.Check(ctx, raw)
		if e != nil {
			return bifrost.Principal{}, e
		}
		return bifrost.Principal{Tenant: id.Tenant(), Account: id.Account(), Device: id.Device(), Expires: id.ExpiresAt()}, nil
	})
	if err != nil {
		return err
	}
	defer handler.Close()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	shared, err := prepareSharedModel(ctx, c.SharedModel, iam.NewSharedTaskServiceClient(ic), c.Origin, c.MemberPasswordFile, c.MemberPolicy)
	if err != nil {
		return err
	}
	if shared != nil {
		defer shared.Close()
	}
	messages, err := prepareSharedMessages(ctx, c.SharedMessages, iam.NewSharedTaskServiceClient(ic))
	if err != nil {
		return err
	}
	if messages != nil {
		defer messages.Close()
	}
	listener, err := net.Listen("tcp", c.Listen)
	if err != nil {
		return err
	}
	defer func() { _ = listener.Close() }()
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 32768, BaseContext: func(net.Listener) context.Context { return ctx }}
	servers := []*http.Server{server}
	if shared != nil {
		servers = append(servers, shared.server)
	}
	if messages != nil {
		servers = append(servers, messages.server)
	}
	done := make(chan error, len(servers))
	go func() {
		if c.TLSCertFile != "" {
			done <- server.ServeTLS(listener, c.TLSCertFile, c.TLSKeyFile)
		} else {
			done <- server.Serve(listener)
		}
	}()
	if shared != nil {
		go func() { done <- shared.server.ServeTLS(shared.listener, "", "") }()
	}
	if messages != nil {
		go func() { done <- messages.server.ServeTLS(messages.listener, "", "") }()
	}
	remaining := len(servers)
	select {
	case err = <-done:
		remaining--
	case <-ctx.Done():
	}
	// Cancel active streams first; SharedProvider keeps a separate settlement
	// context. Do not close the database until the HTTP handlers have ended.
	stop()
	shutdown, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	for _, active := range servers {
		if closeErr := active.Shutdown(shutdown); closeErr != nil {
			_ = active.Close()
			if err == nil || errors.Is(err, http.ErrServerClosed) {
				err = closeErr
			}
		}
	}
	for ; remaining > 0; remaining-- {
		serveErr := <-done
		if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) && (err == nil || errors.Is(err, http.ErrServerClosed)) {
			err = serveErr
		}
	}
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}
