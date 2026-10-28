package main

import (
	"context"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/hansjone/auriga/infra/ai-gateway/internal/team"
	collab "github.com/hansjone/auriga/infra/gen/auriga/collab/v1"
	iam "github.com/hansjone/auriga/infra/gen/auriga/iam/v1"
	org "github.com/hansjone/auriga/infra/gen/auriga/org/v1"
)

// Production Team is required by the normal plugin login lifecycle. This adds
// only its test listener; authentication and every RPC use real IAM/Org/collab.
func startDualTeam(ctx context.Context, ready input, b bridge) (func() error, error) {
	authority, err := team.DialRPC(team.RPCConfig{Target: ready.IAM, ServiceTokenFile: b.GatewayToken})
	if err != nil {
		return nil, err
	}
	mailbox, err := team.DialRPC(team.RPCConfig{Target: ready.Collab, ServiceTokenFile: b.GatewayToken})
	if err != nil {
		_ = authority.Close()
		return nil, err
	}
	contract, err := os.ReadFile(os.Getenv("AURIGA_SEND4_TEAM_CONTRACT"))
	if err != nil {
		_ = authority.Close()
		_ = mailbox.Close()
		return nil, err
	}
	handler, err := team.New(collab.NewCollabCommunicationServiceClient(mailbox),
		team.DeviceAuthenticator(iam.NewDeviceAccessServiceClient(authority), org.NewAuthorizationServiceClient(authority)), contract)
	if err != nil {
		_ = authority.Close()
		_ = mailbox.Close()
		return nil, err
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		_ = authority.Close()
		_ = mailbox.Close()
		return nil, err
	}
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second,
		BaseContext: func(net.Listener) context.Context { return ctx }}
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	closeTeam := func() error {
		closing, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		err := server.Shutdown(closing)
		<-done
		_ = authority.Close()
		_ = mailbox.Close()
		return err
	}
	if err = emit(map[string]any{"stage": "team-ready", "url": "http://" + listener.Addr().String()}); err != nil {
		_ = closeTeam()
		return nil, err
	}
	return closeTeam, nil
}
