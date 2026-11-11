package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/hansjone/auriga/infra/egress/internal/policy"
	"github.com/hansjone/auriga/infra/egress/internal/proxy"
)

var version = "0.0.0-dev"

func run(args []string, out, logs io.Writer) error {
	return runContext(context.Background(), args, out, logs)
}

func runContext(ctx context.Context, args []string, out, logs io.Writer) error {
	flags := flag.NewFlagSet("auriga-egress", flag.ContinueOnError)
	flags.SetOutput(logs)
	showVersion := flags.Bool("version", false, "print version")
	listen := flags.String("listen", "127.0.0.1:3128", "internal CONNECT listen address")
	check := flags.Bool("check", false, "check the local proxy listener")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected arguments: %v", flags.Args())
	}
	if *showVersion {
		_, err := fmt.Fprintln(out, "auriga-egress", version)
		return err
	}
	if *check {
		return checkLocal(ctx)
	}
	server, err := proxy.WithDNSServer(os.Getenv("AURIGA_EGRESS_DNS_SERVER"))
	if err != nil {
		return err
	}
	if err = server.SetDeniedHosts(os.Getenv("AURIGA_EGRESS_DENY_HOSTS")); err != nil {
		return err
	}
	logger := slog.New(slog.NewJSONHandler(logs, nil)).With("service", "egress")
	policyContext, cancelPolicy := context.WithCancel(ctx)
	var policyWorker sync.WaitGroup
	defer func() { cancelPolicy(); policyWorker.Wait() }()
	if source := os.Getenv("AURIGA_EGRESS_POLICY_FILE"); source != "" {
		server.RequirePolicy()
		runner, policyErr := policy.New(source, os.Getenv("AURIGA_EGRESS_STATE_FILE"), os.Getenv("AURIGA_EGRESS_REPORT_FILE"), os.Getenv("AURIGA_EGRESS_NODE_ID"), server)
		if policyErr != nil {
			return policyErr
		}
		policyWorker.Go(func() {
			runner.Run(policyContext, 2*time.Second, func(error) { logger.Warn("policy synchronization failed") })
		})
	}
	listener, err := net.Listen("tcp", *listen)
	if err != nil {
		return err
	}
	logger.Info("egress listening", "version", version, "address", listener.Addr().String())
	return server.Serve(ctx, listener)
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := runContext(ctx, os.Args[1:], os.Stdout, os.Stderr); err != nil {
		slog.New(slog.NewJSONHandler(os.Stderr, nil)).Error("command failed", "error", err)
		os.Exit(1)
	}
}

func checkLocal(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://127.0.0.1:3128/health", nil)
	if err != nil {
		return err
	}
	transport := &http.Transport{Proxy: nil}
	defer transport.CloseIdleConnections()
	response, err := (&http.Client{Transport: transport}).Do(request)
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusNoContent {
		return fmt.Errorf("egress unhealthy")
	}
	return nil
}
