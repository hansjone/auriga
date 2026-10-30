package network

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"os"
	"strings"
	"time"

	"github.com/hansjone/auriga/infra/ai-gateway/internal/team"
	collab "github.com/hansjone/auriga/infra/gen/auriga/collab/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// Same trusted transport and rotating service JWT as team.DialRPC. Its fixed
// 10s interceptor cannot carry the source-control contract's 30s budget. Keep
// the exception here, scoped to the three generated mailbox methods only.
func dialMailboxRPC(c team.RPCConfig) (*grpc.ClientConn, error) {
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
		q, ok := req.(interface {
			GetConnection() *collab.NetworkMailboxConnection
		})
		if !ok || q.GetConnection() == nil {
			return status.Error(codes.Unauthenticated, "network.unauthorized")
		}
		ctx = metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+strings.TrimSpace(string(content)), "auriga-tenant-id", q.GetConnection().GetTenantId())
		timeout := 10 * time.Second
		switch method {
		case collab.NetworkMailboxService_SourceRegister_FullMethodName, collab.NetworkMailboxService_SourceGet_FullMethodName, collab.NetworkMailboxService_SourceRevoke_FullMethodName:
			timeout = sourceRequestTimeout
		}
		bounded, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		return invoke(bounded, method, req, reply, cc, opts...)
	}))
}
