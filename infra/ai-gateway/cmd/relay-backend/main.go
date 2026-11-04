// relay-backend exposes only Auriga's selected Bifrost model and management APIs.
package main

import (
	"context"
	"flag"
	"os"
	"os/signal"
	"syscall"

	"github.com/hansjone/auriga/infra/ai-gateway/internal/bifrostbackend"
	"github.com/hansjone/auriga/infra/ai-gateway/internal/observability"
)

func main() {
	path := flag.String("config", "", "protected relay backend configuration")
	flag.Parse()
	if *path == "" || flag.NArg() != 0 {
		observability.Logger(os.Stderr).Error("relay configuration required")
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if e := bifrostbackend.Serve(ctx, *path); e != nil {
		observability.Logger(os.Stderr).Error("relay backend failed", "error", e)
		os.Exit(1)
	}
}
