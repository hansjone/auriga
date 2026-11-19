package main

import (
	"context"
	"errors"
	"net"
	"net/http"
	"time"

	"github.com/hansjone/auriga/infra/ai-gateway/internal/sharedauth"
	"github.com/hansjone/auriga/infra/ai-gateway/internal/sharedhttp"
	"github.com/hansjone/auriga/infra/ai-gateway/internal/team"
	collab "github.com/hansjone/auriga/infra/gen/auriga/collab/v1"
	iam "github.com/hansjone/auriga/infra/gen/auriga/iam/v1"
	"google.golang.org/grpc"
)

type sharedMessageConfig struct {
	Listen            string            `json:"listen"`
	TenantID          string            `json:"tenantId"`
	AllowedNodeIDs    []string          `json:"allowedNodeIds"`
	MessagePublicKeys map[string]string `json:"messagePublicKeys"`
	Collab            team.RPCConfig    `json:"collab"`
	sharedhttp.TLSFiles
}
type sharedMessageRuntime struct {
	server   *http.Server
	listener net.Listener
	collab   *grpc.ClientConn
}

// This independent listener installs the real mailbox client even while the
// official business signing keyset is temporarily unavailable. Calls fail closed.
func prepareSharedMessages(ctx context.Context, c *sharedMessageConfig, iamClient iam.SharedTaskServiceClient) (*sharedMessageRuntime, error) {
	if c == nil {
		return nil, nil
	}
	if _, _, err := net.SplitHostPort(c.Listen); err != nil {
		return nil, errors.New("invalid shared message listen address")
	}
	keys, err := loadModelPublicKeys(c.MessagePublicKeys)
	if err != nil {
		return nil, err
	}
	tlsConfig, err := sharedhttp.TLSConfig(c.TLSFiles, c.AllowedNodeIDs)
	if err != nil {
		return nil, err
	}
	gate, err := sharedauth.NewRPCGate(iamClient, keys, time.Now)
	if err != nil {
		return nil, err
	}
	cc, err := team.DialRPC(c.Collab)
	if err != nil {
		return nil, err
	}
	handler, err := sharedhttp.NewMessageHandler(c.TenantID, c.AllowedNodeIDs, gate, sharedhttp.RPCMessageCaller{Client: collab.NewSharedMessageServiceClient(cc)})
	if err != nil {
		_ = cc.Close()
		return nil, err
	}
	listener, err := net.Listen("tcp", c.Listen)
	if err != nil {
		_ = cc.Close()
		return nil, err
	}
	server := &http.Server{Handler: handler, TLSConfig: tlsConfig, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 35 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 32768, BaseContext: func(net.Listener) context.Context { return ctx }}
	return &sharedMessageRuntime{server, listener, cc}, nil
}
func (r *sharedMessageRuntime) Close() { _ = r.listener.Close(); _ = r.collab.Close() }
