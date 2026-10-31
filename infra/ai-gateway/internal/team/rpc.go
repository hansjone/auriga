package team

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"github.com/hansjone/auriga/infra/ai-gateway/internal/observability"
	collab "github.com/hansjone/auriga/infra/gen/auriga/collab/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"net"
	"os"
	"strings"
	"time"
)

const requestTimeout = 10 * time.Second

// RPCConfig uses a rotated service JWT file, never the user's device credential.
type RPCConfig struct {
	Target           string
	ServiceTokenFile string
	CertFile         string
	KeyFile          string
	CAFile           string
	ServerName       string
}

func DialRPC(c RPCConfig) (*grpc.ClientConn, error) {
	var transport credentials.TransportCredentials
	if c.CertFile == "" && c.KeyFile == "" && c.CAFile == "" {
		host, _, err := net.SplitHostPort(c.Target)
		if err != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
			return nil, errors.New("plaintext RPC requires numeric loopback")
		}
		transport = insecure.NewCredentials()
	} else {
		cert, err := tls.LoadX509KeyPair(c.CertFile, c.KeyFile)
		if err != nil {
			return nil, err
		}
		pem, err := os.ReadFile(c.CAFile)
		if err != nil {
			return nil, err
		}
		roots := x509.NewCertPool()
		if !roots.AppendCertsFromPEM(pem) {
			return nil, errors.New("invalid RPC trust bundle")
		}
		transport = credentials.NewTLS(&tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots, Certificates: []tls.Certificate{cert}, ServerName: c.ServerName})
	}
	return grpc.NewClient(c.Target, grpc.WithTransportCredentials(transport), grpc.WithUnaryInterceptor(func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoke grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		content, err := os.ReadFile(c.ServiceTokenFile)
		if err != nil || strings.TrimSpace(string(content)) == "" {
			return status.Error(codes.Unavailable, "common.unavailable")
		}
		ctx = metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+strings.TrimSpace(string(content)))
		if actor, ok := ctx.Value(actorKey{}).(*collab.Actor); ok {
			ctx = metadata.AppendToOutgoingContext(ctx, "auriga-tenant-id", actor.TenantId)
		}
		ctx = observability.Outgoing(ctx)
		if _, bounded := ctx.Deadline(); bounded {
			return invoke(ctx, method, req, reply, cc, opts...)
		}
		bounded, cancel := context.WithTimeout(ctx, requestTimeout)
		defer cancel()
		return invoke(bounded, method, req, reply, cc, opts...)
	}))
}

type actorKey struct{}

func WithActor(ctx context.Context, a *collab.Actor) context.Context {
	return context.WithValue(ctx, actorKey{}, a)
}
