package main

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	embeddedpostgres "github.com/fergusstrange/embedded-postgres"
	_ "github.com/lib/pq"
	"github.com/nats-io/nats-server/v2/server"
)

type devOptions struct {
	DataDir, CacheDir, RuntimeDir string
	PGPort                        uint32
	NATSPort                      int
}

type devConnections struct{ Postgres, NATS string }

func (o devOptions) normalized() (devOptions, error) {
	if o.PGPort < 1 || o.PGPort > 65535 || o.NATSPort < 1 || o.NATSPort > 65535 {
		return o, errors.New("ports must be between 1 and 65535")
	}
	if int(o.PGPort) == o.NATSPort {
		return o, errors.New("PostgreSQL and NATS need different ports")
	}
	if o.DataDir == "" {
		return o, errors.New("data directory is required")
	}
	var err error
	o.DataDir, err = filepath.Abs(o.DataDir)
	if err != nil {
		return o, err
	}
	if o.CacheDir == "" {
		o.CacheDir = os.Getenv("AURIGA_PG_CACHE_DIR")
		if o.CacheDir == "" {
			o.CacheDir = filepath.Join(o.DataDir, "cache")
		}
	}
	if o.RuntimeDir == "" {
		o.RuntimeDir = filepath.Join(o.DataDir, "runtime", "pg")
	}
	o.CacheDir, err = filepath.Abs(o.CacheDir)
	if err != nil {
		return o, err
	}
	o.RuntimeDir, err = filepath.Abs(o.RuntimeDir)
	if err != nil {
		return o, err
	}
	// embedded-postgres recreates RuntimePath. It must not overlap persistent data or cache.
	paths := []string{filepath.Join(o.DataDir, "pg"), filepath.Join(o.DataDir, "nats"), o.CacheDir}
	for _, p := range paths {
		if containsPath(o.RuntimeDir, p) || containsPath(p, o.RuntimeDir) {
			return o, errors.New("runtime directory must not overlap PostgreSQL data, NATS data, or archive cache")
		}
	}
	if containsPath(o.RuntimeDir, o.DataDir) {
		return o, errors.New("runtime directory must not contain the data directory")
	}
	return o, nil
}

func containsPath(parent, child string) bool {
	rel, err := filepath.Rel(parent, child)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func credentials(dir string) (string, error) {
	path := filepath.Join(dir, "credentials.env")
	body, err := os.ReadFile(path)
	if err == nil {
		value := strings.TrimSpace(strings.TrimPrefix(string(body), "PGPASSWORD="))
		decoded, decodeErr := hex.DecodeString(value)
		if decodeErr != nil || len(decoded) != 32 || !strings.HasPrefix(string(body), "PGPASSWORD=") {
			return "", errors.New("invalid credentials.env; refusing to reset persisted credentials")
		}
		return value, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if _, err := os.Stat(filepath.Join(dir, "pg", "PG_VERSION")); err == nil {
		return "", errors.New("credentials.env missing for existing PostgreSQL data; restore the credentials file")
	}
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return "", err
	}
	password := hex.EncodeToString(secret)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return "", err
	}
	_, writeErr := fmt.Fprintln(f, "PGPASSWORD="+password)
	return password, errors.Join(writeErr, f.Close())
}

// devServices owns the resources acquired during startup, including partial starts.
type devServices struct {
	pg          *embeddedpostgres.EmbeddedPostgres
	nats        *server.Server
	lockPath    string
	Connections devConnections
}

func (s *devServices) Close() error {
	var result error
	if s.nats != nil {
		s.nats.Shutdown()
		s.nats.WaitForShutdown()
		s.nats = nil
	}
	if s.pg != nil {
		result = errors.Join(result, s.pg.Stop())
		s.pg = nil
	}
	if s.lockPath != "" {
		result = errors.Join(result, os.Remove(s.lockPath))
		s.lockPath = ""
	}
	return result
}

func natsCredentials(dir string) (user, password string, err error) {
	path := filepath.Join(dir, "nats.env")
	body, err := os.ReadFile(path)
	if err == nil {
		user, password = "auriga", ""
		for _, line := range strings.Split(string(body), "\n") {
			line = strings.TrimSpace(line)
			switch {
			case strings.HasPrefix(line, "NATS_USER="):
				user = strings.TrimPrefix(line, "NATS_USER=")
			case strings.HasPrefix(line, "NATS_PASSWORD="):
				password = strings.TrimPrefix(line, "NATS_PASSWORD=")
			}
		}
		if user == "" || password == "" {
			return "", "", errors.New("invalid nats.env; refusing to reset persisted NATS credentials")
		}
		return user, password, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return "", "", err
	}
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return "", "", err
	}
	user, password = "auriga", hex.EncodeToString(secret)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return "", "", err
	}
	_, writeErr := fmt.Fprintf(f, "NATS_USER=%s\nNATS_PASSWORD=%s\n", user, password)
	return user, password, errors.Join(writeErr, f.Close())
}

func authenticatedNATSURL(port int, user, password string) string {
	u := url.URL{
		Scheme: "nats",
		User:   url.UserPassword(user, password),
		Host:   fmt.Sprintf("127.0.0.1:%d", port),
	}
	return u.String()
}

func startNATS(port int, dir, user, password string) (*server.Server, error) {
	if user == "" || password == "" {
		return nil, errors.New("NATS user and password required")
	}
	nats, err := server.NewServer(&server.Options{
		Host: "127.0.0.1", Port: port, JetStream: true, StoreDir: dir,
		Username: user, Password: password,
		NoSigs: true, NoLog: true,
	})
	if err != nil {
		return nil, fmt.Errorf("configure NATS: %w", err)
	}
	nats.Start()
	if !nats.ReadyForConnections(10 * time.Second) {
		nats.Shutdown()
		nats.WaitForShutdown()
		return nil, errors.New("NATS did not become ready (check port availability)")
	}
	return nats, nil
}

func startDev(ctx context.Context, options devOptions, logger *slog.Logger) (_ *devServices, result error) {
	o, err := options.normalized()
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(o.DataDir, 0700); err != nil {
		return nil, err
	}
	services := &devServices{}
	defer func() {
		if result != nil {
			result = errors.Join(result, services.Close())
		}
	}()
	lockPath := filepath.Join(o.DataDir, lockFileName)
	lock, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return nil, fmt.Errorf("lock data directory (another ctl may be running): %w", err)
	}
	services.lockPath = lockPath
	if _, err := fmt.Fprintf(lock, "%d\n", os.Getpid()); err != nil {
		_ = lock.Close()
		_ = os.Remove(lockPath)
		return nil, err
	}
	if err := lock.Close(); err != nil {
		return nil, err
	}
	password, err := credentials(o.DataDir)
	if err != nil {
		return nil, err
	}
	// embedded-postgres would reinitialize incompatible data. Preserve it and fail instead.
	pgVersion, err := os.ReadFile(filepath.Join(o.DataDir, "pg", "PG_VERSION"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if err == nil && strings.TrimSpace(string(pgVersion)) != "17" {
		return nil, errors.New("existing PostgreSQL data is not version 17; use a separate data directory")
	}
	for _, dir := range []string{o.CacheDir, filepath.Dir(o.RuntimeDir)} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			return nil, err
		}
	}
	pg := embeddedpostgres.NewDatabase(embeddedpostgres.DefaultConfig().
		Version(embeddedpostgres.V17).Port(o.PGPort).Username("auriga_dev").Password(password).Database("auriga").Locale("C").Encoding("UTF8").
		DataPath(filepath.Join(o.DataDir, "pg")).CachePath(o.CacheDir).RuntimePath(o.RuntimeDir).
		BinaryRepositoryURL("https://repo.maven.apache.org/maven2").
		StartParameters(map[string]string{"listen_addresses": "127.0.0.1", "password_encryption": "scram-sha-256"}).
		StartTimeout(60 * time.Second).Logger(io.Discard))
	logger.Info("starting PostgreSQL", "port", o.PGPort)
	if err := pg.Start(); err != nil {
		return nil, fmt.Errorf("start PostgreSQL: %w", err)
	}
	services.pg = pg
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	pgURL := (&url.URL{Scheme: "postgresql", User: url.UserPassword("auriga_dev", password), Host: fmt.Sprintf("127.0.0.1:%d", o.PGPort), Path: "/auriga", RawQuery: "sslmode=disable"}).String()
	db, err := sql.Open("postgres", pgURL)
	if err != nil {
		return nil, err
	}
	defer func() { _ = db.Close() }()
	db.SetMaxOpenConns(1)
	if err := db.PingContext(ctx); err != nil {
		return nil, fmt.Errorf("ping PostgreSQL: %w", err)
	}
	if err := ensureServiceRoles(db, o.DataDir, o.PGPort); err != nil {
		return nil, fmt.Errorf("initialize service roles: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	natsUser, natsPassword, err := natsCredentials(o.DataDir)
	if err != nil {
		return nil, err
	}
	services.nats, err = startNATS(o.NATSPort, filepath.Join(o.DataDir, "nats"), natsUser, natsPassword)
	if err != nil {
		return nil, err
	}
	services.Connections = devConnections{
		Postgres: pgURL,
		NATS:     authenticatedNATSURL(o.NATSPort, natsUser, natsPassword),
	}
	return services, nil
}

// runDev keeps both services in the foreground until Ctrl+C cancels the context.
func runDev(ctx context.Context, options devOptions, out io.Writer, logger *slog.Logger) (result error) {
	services, err := startDev(ctx, options, logger)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return nil
		}
		return err
	}
	defer func() { result = errors.Join(result, services.Close()) }()
	c := services.Connections
	dataDir := options.DataDir
	if abs, err := filepath.Abs(dataDir); err == nil {
		dataDir = abs
	}
	if _, err := fmt.Fprintf(out, "PostgreSQL: %s\nNATS: nats://127.0.0.1:%d (auth in %s)\nCredentials: %s\nService roles: %s\nPress Ctrl+C to stop.\nExport AURIGA_NATS_URL (edge) and AURIGA_EVENTS_NATS_URL (platform) from nats.env.\n",
		c.Postgres, options.NATSPort, filepath.Join(dataDir, "nats.env"),
		filepath.Join(dataDir, "credentials.env"), serviceRolesPath(dataDir)); err != nil {
		return err
	}
	logger.Info("development services ready", "pgPort", options.PGPort, "natsPort", options.NATSPort)
	<-ctx.Done()
	logger.Info("stopping development services")
	return nil
}
