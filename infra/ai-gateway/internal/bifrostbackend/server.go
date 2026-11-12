package bifrostbackend

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/hansjone/auriga/infra/ai-gateway/internal/audit"
	"github.com/hansjone/auriga/infra/ai-gateway/internal/store"
	"github.com/hansjone/auriga/infra/gen/deploymentconfig"
)

type PostgreSQL struct {
	Host                  string `json:"host"`
	Port                  int    `json:"port"`
	Database              string `json:"database"`
	User                  string `json:"user"`
	MigrationUser         string `json:"migrationUser"`
	PasswordFile          string `json:"passwordFile"`
	MigrationPasswordFile string `json:"migrationPasswordFile"`
	SSLMode               string `json:"sslMode"`
}
type Config struct {
	AccessFile            string     `json:"accessFile"`
	Listen                string     `json:"listen"`
	PrivateNetwork        bool       `json:"privateNetwork"`
	MemberPasswordFile    string     `json:"memberPasswordFile"`
	AdminPasswordFile     string     `json:"adminPasswordFile"`
	EncryptionKeyFile     string     `json:"encryptionKeyFile"`
	PostgreSQL            PostgreSQL `json:"postgres"`
	AllowPrivateProviders bool       `json:"allowPrivateProviders"`
}

func readSecret(path string) (string, error) {
	info, e := os.Lstat(path)
	if e != nil || !info.Mode().IsRegular() || info.Size() > 128 {
		return "", errors.New("relay secret unavailable")
	}
	raw, e := os.ReadFile(path)
	if e != nil {
		return "", errors.New("relay secret unavailable")
	}
	defer clear(raw)
	s := strings.TrimSpace(string(raw))
	decoded, e := hex.DecodeString(s)
	defer clear(decoded)
	if e != nil || len(decoded) != 32 {
		return "", errors.New("relay secret must be 32 bytes encoded as hex")
	}
	return s, nil
}
func (p PostgreSQL) dsn(user, password string) (string, error) {
	if p.Host == "" || p.Port < 1 || p.Port > 65535 || p.Database == "" || user == "" || !slicesContains([]string{"disable", "require", "verify-ca", "verify-full"}, p.SSLMode) {
		return "", errors.New("invalid relay PostgreSQL configuration")
	}
	u := url.URL{Scheme: "postgres", Host: net.JoinHostPort(p.Host, strconv.Itoa(p.Port)), User: url.UserPassword(user, password), Path: "/" + p.Database}
	q := u.Query()
	q.Set("sslmode", p.SSLMode)
	u.RawQuery = q.Encode()
	return u.String(), nil
}
func slicesContains(items []string, item string) bool {
	for _, s := range items {
		if s == item {
			return true
		}
	}
	return false
}

func Serve(ctx context.Context, path string) error {
	f, e := os.Open(path)
	if e != nil {
		return errors.New("relay configuration unavailable")
	}
	defer func() { _ = f.Close() }()
	var c Config
	d := json.NewDecoder(io.LimitReader(f, 1<<20))
	d.DisallowUnknownFields()
	if d.Decode(&c) != nil || d.Decode(new(any)) != io.EOF {
		return errors.New("invalid relay configuration")
	}
	accessData, e := os.ReadFile(c.AccessFile)
	if e != nil {
		return errors.New("relay deployment identity unavailable")
	}
	access, e := deploymentconfig.Load(accessData)
	if e != nil {
		return errors.New("invalid relay deployment identity")
	}
	host, _, e := net.SplitHostPort(c.Listen)
	if e != nil {
		return errors.New("invalid relay listener")
	}
	ip := net.ParseIP(host)
	if (ip == nil || !ip.IsLoopback()) && !c.PrivateNetwork {
		return errors.New("relay listener requires an explicit private network")
	}
	admin, e := readSecret(c.AdminPasswordFile)
	if e != nil {
		return e
	}
	memberPassword, e := readSecret(c.MemberPasswordFile)
	if e != nil {
		return e
	}
	master, e := readSecret(c.EncryptionKeyFile)
	if e != nil {
		return e
	}
	password, e := readSecret(c.PostgreSQL.PasswordFile)
	if e != nil {
		return e
	}
	migrationPassword, e := readSecret(c.PostgreSQL.MigrationPasswordFile)
	if e != nil {
		return e
	}
	if password == migrationPassword || c.PostgreSQL.User == c.PostgreSQL.MigrationUser {
		return errors.New("relay runtime and migration identities must be distinct")
	}
	dsn, e := c.PostgreSQL.dsn(c.PostgreSQL.User, password)
	if e != nil {
		return e
	}
	migrationDSN, e := c.PostgreSQL.dsn(c.PostgreSQL.MigrationUser, migrationPassword)
	if e != nil {
		return e
	}
	startup, cancel := context.WithTimeout(ctx, 30*time.Second)
	db, e := store.Open(startup, store.Config{DSN: dsn, MigrationDSN: migrationDSN, MaxConnections: 8})
	cancel()
	if e != nil {
		return e
	}
	defer db.Close()
	backend, e := New(db, master, admin, memberPassword, c.AllowPrivateProviders)
	if e != nil {
		return e
	}
	backend.tenant = access.TenantID()
	recoveryCtx, stopRecovery := context.WithCancel(ctx)
	recoveryDone := make(chan struct{})
	go func() { defer close(recoveryDone); backend.recoveryLoop(recoveryCtx) }()
	defer func() { stopRecovery(); <-recoveryDone }()
	if auditURL := os.Getenv("AURIGA_AUDIT_NATS_URL"); auditURL != "" {
		auditDone := make(chan struct{})
		go func() {
			defer close(auditDone)
			if err := audit.Run(recoveryCtx, db, backend.tenant, auditURL); err != nil {
				slog.Error("Audit delivery unavailable; durable records retained")
			}
		}()
		defer func() { stopRecovery(); <-auditDone }()
	} else {
		slog.Warn("Audit delivery paused: AURIGA_AUDIT_NATS_URL is required to forward durable records")
	}

	listener, e := net.Listen("tcp", c.Listen)
	if e != nil {
		return errors.New("relay listener unavailable")
	}
	defer func() { _ = listener.Close() }()
	server := &http.Server{Handler: backend, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 32768}
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	select {
	case e = <-done:
		if errors.Is(e, http.ErrServerClosed) {
			return nil
		}
		return e
	case <-ctx.Done():
	}
	shutdown, stop := context.WithTimeout(context.Background(), 160*time.Second)
	defer stop()
	e = server.Shutdown(shutdown)
	if e != nil {
		_ = server.Close()
	}
	if serveErr := <-done; e == nil && !errors.Is(serveErr, http.ErrServerClosed) {
		e = serveErr
	}
	return e
}
