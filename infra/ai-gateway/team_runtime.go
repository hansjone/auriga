package main

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/hansjone/auriga/infra/ai-gateway/internal/team"
	collab "github.com/hansjone/auriga/infra/gen/auriga/collab/v1"
	iam "github.com/hansjone/auriga/infra/gen/auriga/iam/v1"
	org "github.com/hansjone/auriga/infra/gen/auriga/org/v1"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

type teamConfig struct {
	Listen      string         `json:"listen"`
	TLSCertFile string         `json:"tlsCertFile"`
	TLSKeyFile  string         `json:"tlsKeyFile"`
	SchemaFile  string         `json:"schemaFile"`
	IAM         team.RPCConfig `json:"iam"`
	Org         team.RPCConfig `json:"org"`
	Collab      team.RPCConfig `json:"collab"`
}

func serveTeam(configFile string) error {
	file, err := os.Open(configFile)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	var cfg teamConfig
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&cfg); err != nil {
		return err
	}
	if decoder.Decode(new(any)) != io.EOF {
		return errors.New("trailing configuration")
	}
	if cfg.Listen == "" {
		cfg.Listen = "127.0.0.1:0"
	}
	host, _, err := net.SplitHostPort(cfg.Listen)
	if err != nil {
		return err
	}
	if cfg.TLSCertFile == "" || cfg.TLSKeyFile == "" {
		if net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
			return errors.New("non-loopback Team listener requires TLS")
		}
	}
	schema, err := os.ReadFile(cfg.SchemaFile)
	if err != nil {
		return err
	}
	iamConn, err := team.DialRPC(cfg.IAM)
	if err != nil {
		return err
	}
	defer func() { _ = iamConn.Close() }()
	orgConn, err := team.DialRPC(cfg.Org)
	if err != nil {
		return err
	}
	defer func() { _ = orgConn.Close() }()
	collabConn, err := team.DialRPC(cfg.Collab)
	if err != nil {
		return err
	}
	defer func() { _ = collabConn.Close() }()
	handler, err := team.New(collab.NewCollabCommunicationServiceClient(collabConn), team.DeviceAuthenticator(iam.NewDeviceAccessServiceClient(iamConn), org.NewAuthorizationServiceClient(orgConn)), schema)
	if err != nil {
		return err
	}
	listener, err := net.Listen("tcp", cfg.Listen)
	if err != nil {
		return err
	}
	defer func() { _ = listener.Close() }()
	server := &http.Server{Handler: team.Deadline(handler), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 15 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 32768}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	completed := make(chan error, 1)
	go func() {
		if cfg.TLSCertFile != "" {
			completed <- server.ServeTLS(listener, cfg.TLSCertFile, cfg.TLSKeyFile)
		} else {
			completed <- server.Serve(listener)
		}
	}()
	select {
	case err = <-completed:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return server.Shutdown(shutdown)
	}
}
