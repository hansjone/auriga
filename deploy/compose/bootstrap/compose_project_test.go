package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestSealedComposeProject(t *testing.T) {
	f := activationFixture(t)
	before := originalBase(t, f.out)
	run := func(args ...string) (string, error) {
		b, err := exec.Command(f.binary, args...).CombinedOutput()
		return string(b), err
	}
	for _, args := range [][]string{
		{"-print-compose-project", "-out", f.out},
		append(append([]string{}, f.args...), "-validate-access", "-print-compose-project"),
	} {
		if b, err := run(args...); err != nil || b != "public-test\n" {
			t.Fatalf("sealed project: %v %s", err, b)
		}
	}
	assertBase(t, f.out, before)
	for _, name := range []string{".collab-activation.lock", ".collab-activation.pending"} {
		p := filepath.Join(f.out, name)
		if err := os.WriteFile(p, []byte("interrupted fixture"), 0600); err != nil {
			t.Fatal(err)
		}
		if b, err := run("-print-compose-project", "-out", f.out); err != nil || b != "public-test\n" {
			t.Fatalf("base inspection unavailable for bounded stop: %v %s", err, b)
		}
		if err := os.Remove(p); err != nil {
			t.Fatal(err)
		}
	}
	access := filepath.Join(f.out, "access.json")
	raw, err := os.ReadFile(access)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(access, append(raw, '\n'), 0644); err != nil {
		t.Fatal(err)
	}
	if b, err := run("-print-compose-project", "-out", f.out); err == nil || strings.Contains(b, "public-test\n") {
		t.Fatalf("unsealed access emitted a project: %v %s", err, b)
	}
	if err := os.WriteFile(access, raw, 0644); err != nil {
		t.Fatal(err)
	}
	if b, err := run("-print-compose-project", "-out", t.TempDir()); err == nil || strings.Contains(b, "public-test\n") {
		t.Fatalf("missing installation emitted project: %v %s", err, b)
	}
	args := append(append([]string{}, f.args...), "-validate-access", "-print-compose-project")
	args[len(f.args)-3] = strings.Repeat("0", 64)
	if b, err := run(args...); err == nil || strings.Contains(b, "public-test\n") {
		t.Fatalf("wrong independent checkpoint emitted project: %v %s", err, b)
	}
	assertBase(t, f.out, before)
}

// Actual product scripts run under an explicit Git Bash on Windows. Only docker,
// projection and the write/check activation operations are fake; project lookup
// and the initial checkpoint/input preflight execute the real fixture binary.
func TestActivationComposeProjectBinding(t *testing.T) {
	f := activationFixture(t)
	root := filepath.Dir(f.out)
	scripts, bin := filepath.Join(root, "scripts"), filepath.Join(root, "bin")
	for _, dir := range []string{scripts, bin} {
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	write := func(path, body string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(body), 0700); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"compose.sh", "activate-collab.sh"} {
		b, err := os.ReadFile("../scripts/" + name)
		if err != nil {
			t.Fatal(err)
		}
		write(filepath.Join(scripts, name), string(b))
	}
	write(filepath.Join(scripts, "collab-materials.sh"), "#!/bin/sh\nprintf 'PROJECTION\\n' >> \"$AURIGA_TEST_LOG\"\n")
	write(filepath.Join(bin, "id"), "#!/bin/sh\necho 0\n")
	write(filepath.Join(bin, "uname"), "#!/bin/sh\necho Linux\n")
	write(filepath.Join(bin, "go"), `#!/bin/sh
shift 4
printf 'GO|%s\n' "$*" >> "$AURIGA_TEST_LOG"
case " $* " in
  *" -print-compose-project "*) exec "$AURIGA_TEST_BOOTSTRAP" "$@" ;;
  *" -stopped-confirmed "*) printf 'GENERATE\n' >> "$AURIGA_TEST_LOG"; mkdir -p "$AURIGA_TEST_OUT/collab-activation" ;;
  *" -check-collab-activation "*) exit 0 ;;
  *) exec "$AURIGA_TEST_BOOTSTRAP" "$@" ;;
esac
`)
	write(filepath.Join(bin, "docker"), `#!/bin/sh
project=; next=false; ps=false
for arg in "$@"; do
  if [ "$next" = true ]; then project=$arg; next=false; fi
  [ "$arg" != --project-name ] || next=true
  [ "$arg" != ps ] || ps=true
done
printf 'DOCKER|project=%s|env=%s|file=%s|envfiles=%s|profiles=%s|args=%s\n' "$project" "${COMPOSE_PROJECT_NAME:-}" "${COMPOSE_FILE:-}" "${COMPOSE_ENV_FILES:-}" "${COMPOSE_PROFILES:-}" "$*" >> "$AURIGA_TEST_LOG"
if [ "$ps" = true ] && [ "$AURIGA_TEST_RUNNING" = 1 ] && [ "$project" = public-test ]; then echo sealed-container; fi
`)
	write(filepath.Join(f.out, "compose.env"), "COMPOSE_PROJECT_NAME=foreign-file\nCOMPOSE_FILE=foreign-file.yml\nCOMPOSE_PROFILES=foreign-file-profile\n")
	log := filepath.Join(root, "wrapper.log")
	selectors := []string{"COMPOSE_PROJECT_NAME=foreign-example", "COMPOSE_FILE=foreign.yml", "COMPOSE_ENV_FILES=foreign.env", "COMPOSE_PROFILES=foreign-profile", "COMPOSE_PATH_SEPARATOR=,", "COMPOSE_DISABLE_ENV_FILE=0"}
	bash := "bash"
	if runtime.GOOS == "windows" {
		bash = `C:\Program Files\Git\bin\bash.exe`
		if _, err := os.Stat(bash); err != nil {
			t.Fatalf("explicit Git Bash required, never WSL: %v", err)
		}
	}
	bashPath := func(path string) string {
		path = filepath.ToSlash(path)
		if runtime.GOOS == "windows" {
			return "/" + strings.ToLower(path[:1]) + path[2:]
		}
		return path
	}
	run := func(script string, running bool, args ...string) (string, string, error) {
		t.Helper()
		write(log, "")
		argv := []string{"--noprofile", "--norc", "-c", `export PATH="$AURIGA_TEST_BIN:/usr/bin:/bin"; exec bash --noprofile --norc "$@"`, "fixture", bashPath(filepath.Join(scripts, script))}
		argv = append(argv, args...)
		cmd := exec.Command(bash, argv...)
		state := "0"
		if running {
			state = "1"
		}
		for _, entry := range os.Environ() {
			key, _, _ := strings.Cut(entry, "=")
			if !strings.HasPrefix(strings.ToUpper(key), "COMPOSE_") {
				cmd.Env = append(cmd.Env, entry)
			}
		}
		cmd.Env = append(cmd.Env, "AURIGA_TEST_BIN="+bashPath(bin), "AURIGA_TEST_LOG="+bashPath(log), "AURIGA_TEST_BOOTSTRAP="+bashPath(f.binary), "AURIGA_TEST_OUT="+bashPath(f.out), "AURIGA_TEST_RUNNING="+state)
		cmd.Env = append(cmd.Env, selectors...)
		b, err := cmd.CombinedOutput()
		recorded, e := os.ReadFile(log)
		if e != nil {
			t.Fatal(e)
		}
		return string(b), string(recorded), err
	}
	assertDocker := func(t *testing.T, log string) {
		t.Helper()
		found := false
		for _, line := range strings.Split(log, "\n") {
			if strings.HasPrefix(line, "DOCKER|") {
				found = true
				if !strings.HasPrefix(line, "DOCKER|project=public-test|env=public-test|file=|envfiles=|") || strings.Contains(line, "foreign") {
					t.Fatalf("unbound Compose selection: %s", line)
				}
			}
		}
		if !found {
			t.Fatalf("no fake Docker call: %s", log)
		}
	}
	t.Run("clean-environment", func(t *testing.T) {
		foreign := selectors
		selectors = nil
		defer func() { selectors = foreign }()
		b, log, err := run("compose.sh", false, "config", "--quiet")
		if err != nil {
			t.Fatalf("normal sealed project: %v %s %s", err, b, log)
		}
		assertDocker(t, log)
	})
	for _, action := range []string{"activation-stop", "activation-running", "up", "start", "stop", "restart", "down", "ps", "logs", "config", "build", "exec", "bootstrap-registry"} {
		t.Run(action, func(t *testing.T) {
			b, log, err := run("compose.sh", false, action)
			if err != nil {
				t.Fatalf("wrapper: %v %s %s", err, b, log)
			}
			assertDocker(t, log)
		})
	}
	for _, selector := range [][]string{{"--project-name", "other"}, {"--project-name=other"}, {"-p", "other"}, {"-pother"}, {"-p=other"}, {"--file", "foreign.yml"}, {"--file=foreign.yml"}, {"-f", "foreign.yml"}, {"-fforeign.yml"}, {"-f=foreign.yml"}, {"--env-file", "foreign.env"}, {"--env-file=foreign.env"}, {"--profile", "foreign"}, {"--profile=foreign"}, {"--project-directory", "foreign"}, {"--project-directory=foreign"}} {
		t.Run(strings.Join(selector, "_"), func(t *testing.T) {
			b, log, err := run("compose.sh", false, append([]string{"config"}, selector...)...)
			if err == nil || strings.Contains(log, "DOCKER|") {
				t.Fatalf("selector reached Docker: %v %s %s", err, b, log)
			}
		})
	}
	t.Run("preflight-project-mismatch", func(t *testing.T) {
		if b, log, err := run("compose.sh", false, "--expected-project", "other", "ps"); err == nil || strings.Contains(log, "DOCKER|") {
			t.Fatalf("changed preflight project accepted: %v %s %s", err, b, log)
		}
	})
	t.Run("pending-bounded-stop", func(t *testing.T) {
		lock := filepath.Join(f.out, ".collab-activation.lock")
		write(lock, "busy fixture")
		defer func() { _ = os.Remove(lock) }()
		if b, log, err := run("compose.sh", false, "activation-stop"); err != nil {
			t.Fatalf("bounded stop: %v %s %s", err, b, log)
		} else {
			assertDocker(t, log)
		}
		if b, log, err := run("compose.sh", false, "up"); err == nil || strings.Contains(log, "DOCKER|") {
			t.Fatalf("pending activation started: %v %s %s", err, b, log)
		}
	})
	// Project B is empty, but the sealed project's ps reports a running process.
	t.Run("sealed-project-still-running", func(t *testing.T) {
		if b, log, err := run("activate-collab.sh", true, bashPath(f.input), f.base, f.identity); err == nil || !strings.Contains(b, "still running") || strings.Contains(log, "GENERATE") {
			t.Fatalf("foreign emptiness became stopped confirmation: %v %s %s", err, b, log)
		} else {
			assertDocker(t, log)
		}
	})
	t.Run("stopped-project-config-dispatch", func(t *testing.T) {
		if b, log, err := run("activate-collab.sh", false, bashPath(f.input), f.base, f.identity); err != nil || !strings.Contains(log, "GENERATE") || !strings.Contains(log, "config --quiet") {
			t.Fatalf("fixed project activation dispatch: %v %s %s", err, b, log)
		} else {
			assertDocker(t, log)
		}
	})
	t.Run("unsealed-access-no-docker", func(t *testing.T) {
		path := filepath.Join(f.out, "access.json")
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = os.WriteFile(path, raw, 0644) }()
		if err := os.WriteFile(path, append(raw, '\n'), 0644); err != nil {
			t.Fatal(err)
		}
		if b, log, err := run("compose.sh", false, "activation-running"); err == nil || strings.Contains(log, "DOCKER|") {
			t.Fatalf("unsealed project reached Docker: %v %s %s", err, b, log)
		}
	})
}
