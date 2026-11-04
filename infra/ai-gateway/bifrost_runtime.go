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
	listener, err := net.Listen("tcp", c.Listen)
	if err != nil {
		return err
	}
	defer func() { _ = listener.Close() }()
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 32768}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	done := make(chan error, 1)
	go func() {
		if c.TLSCertFile != "" {
			done <- server.ServeTLS(listener, c.TLSCertFile, c.TLSKeyFile)
		} else {
			done <- server.Serve(listener)
		}
	}()
	select {
	case err = <-done:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		err = server.Shutdown(shutdown)
		if err != nil {
			_ = server.Close()
		}
		serveErr := <-done
		if err != nil {
			return err
		}
		if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			return serveErr
		}
		return nil
	}
}
