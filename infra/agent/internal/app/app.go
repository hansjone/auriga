// Package app wires auriga-agent commands (join / run).
package app

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/hansjone/auriga/infra/agent/internal/artifacts"
	"github.com/hansjone/auriga/infra/agent/internal/config"
	"github.com/hansjone/auriga/infra/agent/internal/conn"
	"github.com/hansjone/auriga/infra/agent/internal/enroll"
	"github.com/hansjone/auriga/infra/agent/internal/identity"
	"github.com/hansjone/auriga/infra/agent/internal/ingress"
	"github.com/hansjone/auriga/infra/agent/internal/observer"
	"github.com/hansjone/auriga/infra/agent/internal/policy"
	"github.com/hansjone/auriga/infra/agent/internal/reconciler"
	"github.com/hansjone/auriga/infra/agent/internal/runtime"
	"github.com/hansjone/auriga/infra/agent/internal/state"
	"github.com/hansjone/auriga/infra/agent/internal/storage"
	nodev1 "github.com/hansjone/auriga/infra/gen/auriga/node/v1"
)

// Version is set by main.
var Version = "0.0.0-dev"

// RunCLI parses args and executes join / run / --version.
func RunCLI(args []string, out, logs io.Writer) error {
	if len(args) == 0 {
		logger := slog.New(slog.NewJSONHandler(logs, nil)).With("service", "agent")
		logger.Info("agent ready", "version", Version, "hint", "use join or run")
		return nil
	}
	if args[0] == "--version" || args[0] == "-version" {
		_, err := fmt.Fprintln(out, "auriga-agent", Version)
		return err
	}

	switch args[0] {
	case "join":
		return runJoin(args[1:], logs)
	case "run":
		return runAgent(args[1:], logs)
	default:
		return fmt.Errorf("unknown command %q (want join|run|--version)", args[0])
	}
}

func runJoin(args []string, logs io.Writer) error {
	fs := flag.NewFlagSet("join", flag.ContinueOnError)
	fs.SetOutput(logs)
	server := fs.String("server", "", "platform URL (https://host:port)")
	token := fs.String("token", "", "one-time enrollment token (prefer --token-file or env)")
	tokenFile := fs.String("token-file", "", "read token from file")
	caSHA := fs.String("ca-sha256", "", "platform CA SPKI SHA-256 (hex)")
	configPath := fs.String("config", "", "optional JSON config path")
	dataRoot := fs.String("data-root", "", "override data root")
	force := fs.Bool("force", false, "replace existing enrollment")
	allowInsecure := fs.Bool("allow-insecure", false, "allow grpc:// http:// (local dev only)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	if *dataRoot != "" {
		cfg.DataRoot = *dataRoot
	}
	if *server != "" {
		cfg.Server = *server
	}
	cfg.AgentVersion = Version
	if *allowInsecure {
		cfg.AllowInsecure = true
	}
	tok, err := enroll.ReadToken(*token, *tokenFile)
	if err != nil {
		return err
	}
	logger := slog.New(slog.NewJSONHandler(logs, nil)).With("service", "agent")
	if err := ensureLayout(cfg); err != nil {
		return err
	}
	_, err = enroll.Join(context.Background(), enroll.Options{
		Server:        cfg.Server,
		Token:         tok,
		CAFingerprint: *caSHA,
		DataRoot:      cfg.DataRoot,
		AgentDir:      cfg.AgentDir(),
		AgentVersion:  cfg.AgentVersion,
		Logger:        logger,
		AllowInsecure: cfg.AllowInsecure,
		Force:         *force,
	})
	return err
}

func runAgent(args []string, logs io.Writer) error {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(logs)
	configPath := fs.String("config", "", "optional JSON config path")
	dataRoot := fs.String("data-root", "", "override data root")
	allowInsecure := fs.Bool("allow-insecure", false, "allow grpc:// http:// (local dev only)")
	ingressListen := fs.String("ingress-listen", "", "TCP listen for edge mTLS (multi-node); empty = local only")
	ingressLocal := fs.String("ingress-local", "", "unix socket or Windows pipe name for single-node ingress")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	if *dataRoot != "" {
		cfg.DataRoot = *dataRoot
	}
	if *allowInsecure {
		cfg.AllowInsecure = true
	}
	cfg.AgentVersion = Version
	logger := slog.New(slog.NewJSONHandler(logs, nil)).With("service", "agent")
	if err := ensureLayout(cfg); err != nil {
		return err
	}

	store, err := state.Open(cfg.AgentDir())
	if err != nil {
		return err
	}
	defer func() { _ = store.Close() }()

	driverName := runtime.PrimaryDriverName(cfg.Drivers)
	// config.Default still lists "fake" (A1). For `run` with no drivers override,
	// A2 defaults to Docker. Explicit AURIGA_AGENT_DRIVERS or JSON drivers win.
	if os.Getenv("AURIGA_AGENT_DRIVERS") == "" && *configPath == "" && driverName == "fake" {
		driverName = "docker"
	}
	polPath := os.Getenv("AURIGA_AGENT_POLICY_FILE")
	if polPath == "" {
		polPath = filepath.Join(cfg.DataRoot, "agent-policy.yaml")
	}
	pol, err := policy.Load(polPath)
	if err != nil {
		return fmt.Errorf("load policy: %w", err)
	}
	inner, err := runtime.SelectDriver(driverName, runtime.DockerConfig{DataRoot: cfg.DataRoot, Policy: pol})
	if err != nil {
		return err
	}
	driver := runtime.Driver(inner)
	var prep *runtime.Preparing
	if driverName != "fake" {
		art := artifacts.New(cfg.ArtifactsDir(), pol)
		prep = &runtime.Preparing{
			Inner:       inner,
			Specs:       store,
			Artifacts:   art,
			Layout:      &storage.Layout{InstancesDir: cfg.InstancesDir()},
			UIDs:        storage.NewUIDTable(cfg.AgentDir()),
			Policy:      pol,
			PlatformURL: cfg.Server,
			Chown:       storage.Lchown,
		}
		if mat, loadErr := identity.Load(cfg.AgentDir()); loadErr == nil {
			prep.SealingPrivate = mat.SealingPrivate
		}
		driver = prep
	}
	rec := reconciler.New(store, driver, logger, cfg.MaxConcurrentPrepare)
	defer rec.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if prep != nil {
		gcEvery, err := artifactGCInterval()
		if err != nil {
			return err
		}
		prep.Log = logger
		defer prep.StartGC(ctx, gcEvery)()
	}

	reg := ingress.NewMapRegistry()
	targets := &observer.SliceTargets{}
	sess := conn.New(conn.Config{
		AgentDir:      cfg.AgentDir(),
		AgentVersion:  cfg.AgentVersion,
		ControlURL:    cfg.ControlEndpoint,
		Drivers:       []nodev1.RuntimeKind{nodev1.RuntimeKind_RUNTIME_KIND_DOCKER},
		Logger:        logger,
		Reconciler:    rec,
		Store:         store,
		AllowInsecure: cfg.AllowInsecure,
		Capacity:      helloCapacity(cfg.DetectCapacity()),
	})
	io, err := startIngressAndObserver(ctx, cfg, logger, *ingressListen, *ingressLocal, reg, targets, sess)
	if err != nil {
		return err
	}
	defer io.Stop()
	sess.SetIngressEndpoint(io.Endpoint)

	bridge := newInstanceBridge(cfg, driver, reg, targets)
	statusSink := sess.StatusSink()
	rec.Sink = func(st *nodev1.InstanceStatus) {
		bridge.Sync(st)
		statusSink(st)
	}

	logger.Info("agent starting", "version", Version, "data_root", cfg.DataRoot, "driver", driverName)
	return sess.Run(ctx)
}

// defaultArtifactGCInterval is how often cached artifacts are collected
// besides the collection after each prepare.
const defaultArtifactGCInterval = 6 * time.Hour

// artifactGCInterval reads AURIGA_AGENT_ARTIFACT_GC_INTERVAL (Go duration,
// "0" disables the periodic run).
func artifactGCInterval() (time.Duration, error) {
	raw := os.Getenv("AURIGA_AGENT_ARTIFACT_GC_INTERVAL")
	if raw == "" {
		return defaultArtifactGCInterval, nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d < 0 {
		return 0, fmt.Errorf("AURIGA_AGENT_ARTIFACT_GC_INTERVAL: invalid duration %q", raw)
	}
	return d, nil
}

func ensureLayout(cfg config.Config) error {
	for _, dir := range []string{cfg.AgentDir(), cfg.InstancesDir(), cfg.ArtifactsDir(), cfg.BackupsDir()} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("mkdir %s: %w", dir, err)
		}
	}
	_ = os.WriteFile(filepath.Join(cfg.AgentDir(), ".layout"), []byte("auriga-agent\n"), 0o644)
	return nil
}

func helloCapacity(c config.Capacity) *nodev1.Capacity {
	out := &nodev1.Capacity{CpuMillis: c.CPUMillis, MemoryMb: c.MemoryMB, DiskGb: c.DiskGB}
	if c.MaxInstances > 0 {
		max := c.MaxInstances
		out.MaxInstances = &max
	}
	return out
}
