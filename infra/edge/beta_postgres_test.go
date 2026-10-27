package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
)

// Optional native-PG integration, not a production IAM/login fixture. Root's real-browser check
// still uses DevBootstrap and real IAM. Every SQL result stays in this protected test directory.
func TestBetaPostgresProjectionReadsRevocationAndRejectsOtherTenants(t *testing.T) {
	bin := os.Getenv("AURIGA_BETA_TEST_PG_BIN")
	if bin == "" {
		t.Skip("set AURIGA_BETA_TEST_PG_BIN to run isolated native PostgreSQL integration")
	}
	dir := t.TempDir()
	if runtime.GOOS == "windows" {
		script, err := filepath.Abs("../../scripts/beta-ui/private.ps1")
		if err != nil {
			t.Fatal(err)
		}
		if err = exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-File", script, "-Path", dir).Run(); err != nil {
			t.Fatal("protect test directory:", err)
		}
	}
	tool := func(name string) string {
		if runtime.GOOS == "windows" {
			name += ".exe"
		}
		return filepath.Join(bin, name)
	}
	data := filepath.Join(dir, "data")
	run := func(name string, args ...string) error {
		output, err := exec.Command(tool(name), args...).CombinedOutput()
		if err != nil {
			t.Logf("native PG lifecycle failed: %s", output)
		}
		return err
	}
	if err := run("initdb", "-D", data, "-U", "beta_admin", "-A", "trust", "--no-locale", "-E", "UTF8"); err != nil {
		t.Fatal(err)
	}
	socket, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := socket.Addr().(*net.TCPAddr).Port
	_ = socket.Close()
	t.Cleanup(func() {
		if err := run("pg_ctl", "-D", data, "-m", "immediate", "-w", "stop"); err != nil {
			t.Error("failed to stop owned test PostgreSQL:", err)
		}
	})
	if err := run("pg_ctl", "-D", data, "-l", filepath.Join(dir, "postgres.log"), "-o", fmt.Sprintf("-h 127.0.0.1 -p %d", port), "-w", "-t", "10", "start"); err != nil {
		t.Fatal(err)
	}
	psql := tool("psql")
	sql := func(user, database, query string) ([]byte, error) {
		output, fileErr := os.CreateTemp(dir, "query-*.out")
		if fileErr != nil {
			return nil, fileErr
		}
		_ = output.Close()
		defer func() { _ = os.Remove(output.Name()) }()
		command := exec.Command(psql, "-X", "-q", "-w", "-A", "-t", "-v", "ON_ERROR_STOP=1", "-h", "127.0.0.1", "-p", strconv.Itoa(port), "-U", user, "-d", database, "-o", output.Name())
		input, fileErr := os.CreateTemp(dir, "query-*.sql")
		if fileErr != nil {
			return nil, fileErr
		}
		defer func() { _ = os.Remove(input.Name()) }()
		_, fileErr = input.WriteString(query)
		_ = input.Close()
		if fileErr != nil {
			return nil, fileErr
		}
		command.Args = append(command.Args, "-f", input.Name())
		if err := command.Run(); err != nil {
			return nil, err
		}
		return os.ReadFile(output.Name())
	}
	database := "auriga_beta_0123456789"
	owner, reader := database+"_iam", database+"_edgekv"
	if _, err := sql("beta_admin", "postgres", "CREATE ROLE "+owner+" LOGIN NOSUPERUSER NOBYPASSRLS; CREATE ROLE "+reader+" LOGIN NOSUPERUSER NOBYPASSRLS; ALTER ROLE "+reader+" SET default_transaction_read_only=on; CREATE DATABASE "+database+";"); err != nil {
		t.Fatal("create isolated test roles/database:", err)
	}
	if _, err := sql("beta_admin", database, "CREATE SCHEMA iam AUTHORIZATION "+owner+"; GRANT CREATE ON DATABASE "+database+" TO "+owner+";"); err != nil {
		t.Fatal(err)
	}
	if _, err := sql(owner, database, `CREATE TABLE iam.session_kv(tenant_id uuid NOT NULL, lookup_key text NOT NULL, data jsonb NOT NULL, expires_at timestamptz NOT NULL); ALTER TABLE iam.session_kv ENABLE ROW LEVEL SECURITY; ALTER TABLE iam.session_kv FORCE ROW LEVEL SECURITY; CREATE POLICY tenant ON iam.session_kv USING (tenant_id=NULLIF(current_setting('auriga.tenant_id',true),'')::uuid);`); err != nil {
		t.Fatal(err)
	}
	// Use the actual installer view DDL; its owner remains the non-BYPASSRLS IAM role.
	ddl := filepath.Join(dir, "view.sql")
	installer, err := filepath.Abs("../../scripts/beta-ui/runner.mjs")
	if err != nil {
		t.Fatal(err)
	}
	manifest, _ := json.Marshal(map[string]any{"database": database, "roles": map[string]string{"iam": owner, "edgeKv": reader}})
	generator := `const fs=require('fs'),{pathToFileURL}=require('url'); import(pathToFileURL(process.argv[1]).href).then(m=>fs.writeFileSync(process.argv[2],m.sessionProjectionDDL(JSON.parse(process.argv[3])))).catch(()=>process.exit(1));`
	if err := exec.Command("node", "-e", generator, installer, ddl, string(manifest)).Run(); err != nil {
		t.Fatal(err)
	}
	view, err := os.ReadFile(ddl)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sql(owner, database, string(view)); err != nil {
		t.Fatal("install view:", err)
	}
	g, _, memory := accessFixtureConfigured(t, false, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) }), func(_ *Config, access map[string]any) { access["tenantId"] = "00000000-0000-0000-0000-000000000001" })
	backend, _ := browserRPCFixture(t, g, memory)
	row := putBrowserSession(g, backend, "unit-cookie", "admin")
	backend.project = false
	value, err := json.Marshal(map[string]any{"value": row})
	if err != nil {
		t.Fatal(err)
	}
	hash := HashCookie("unit-cookie")
	insert := "INSERT INTO iam.session_kv VALUES ('" + g.Cfg.Access.TenantID() + "','" + hash + "','" + string(value) + "',CURRENT_TIMESTAMP+INTERVAL '1 hour');"
	if _, err := sql("beta_admin", database, insert); err != nil {
		t.Fatal("insert test projection:", err)
	}
	pass := filepath.Join(dir, "reader.pgpass")
	if err := os.WriteFile(pass, []byte("# isolated test cluster uses trust only on its random loopback port\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(dir, "reader.json")
	encoded, err := json.Marshal(betaSessionKVConfig{Psql: psql, Port: port, Database: database, Username: reader, PassFile: pass, Directory: dir, TenantID: g.Cfg.Access.TenantID()})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := openBetaSessionStore(config, g.Cfg)
	if err != nil {
		t.Fatal(err)
	}
	g.Sessions = store
	if response := browserRequest(g, "admin", "GET", "/api/iam/v1/me", "unit-cookie"); response.Code != 204 {
		t.Fatalf("real shared PG projection still rejected: %d", response.Code)
	}
	if _, err := sql(reader, database, "SELECT * FROM iam.session_kv;"); err == nil {
		t.Fatal("reader accessed IAM schema directly")
	}
	if _, err := sql(reader, database, "INSERT INTO edge_session_cache.sessions VALUES (NULL,NULL,NULL,NULL);"); err == nil {
		t.Fatal("reader can write projection")
	}
	other := "00000000-0000-0000-0000-000000000002"
	if _, err := sql("beta_admin", database, "INSERT INTO iam.session_kv VALUES ('"+other+"','"+hash+"','"+string(value)+"',CURRENT_TIMESTAMP+INTERVAL '1 hour'); DELETE FROM iam.session_kv WHERE tenant_id='"+g.Cfg.Access.TenantID()+"';"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get(context.Background(), hash); err != ErrSessionNotFound {
		t.Fatalf("cross-tenant projection was returned: %v", err)
	}
	delete(backend.rows, "unit-cookie")
	if response := browserRequest(g, "admin", "GET", "/api/iam/v1/me", "unit-cookie"); response.Code != 401 {
		t.Fatalf("revoked old cookie accepted: %d", response.Code)
	}
	for _, pattern := range []string{"projection-*.json"} {
		paths, err := filepath.Glob(filepath.Join(dir, pattern))
		if err != nil || len(paths) != 0 {
			t.Fatal("private result files leaked")
		}
	}
}
