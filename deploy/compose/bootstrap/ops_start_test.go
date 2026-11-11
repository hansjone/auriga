package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestComposePreparesOpsBeforePlatformAndPreservesFailure(t *testing.T) {
	root := t.TempDir()
	scripts := filepath.Join(root, "scripts")
	bin := filepath.Join(root, "bin")
	for _, name := range []string{scripts, bin, filepath.Join(root, "var")} {
		if err := os.Mkdir(name, 0700); err != nil {
			t.Fatal(err)
		}
	}
	script, err := os.ReadFile("../scripts/compose.sh")
	if err != nil {
		t.Fatal(err)
	}
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(name, []byte(body), 0700); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(scripts, "compose.sh"), strings.ReplaceAll(string(script), "\r\n", "\n"))
	write(filepath.Join(root, "var", "compose.env"), "fixture=1\n")
	write(filepath.Join(bin, "go"), "#!/bin/sh\nprintf 'ops-test\n'\n")
	write(filepath.Join(bin, "docker"), `#!/bin/sh
printf '%s\n' "$*" >>"$OPS_START_LOG"
case " $* " in *' exec -T postgres psql '*) exit "${OPS_ROLE_EXIT:-0}" ;; esac
`)
	bash := "bash"
	if runtime.GOOS == "windows" {
		bash = "C:/Program Files/Git/bin/bash.exe"
	}
	slash := func(p string) string {
		p = filepath.ToSlash(p)
		if runtime.GOOS == "windows" {
			return "/" + strings.ToLower(p[:1]) + p[2:]
		}
		return p
	}
	log := filepath.Join(root, "commands.log")
	for _, failure := range []string{"0", "73"} {
		write(log, "")
		cmd := exec.Command(bash, filepath.ToSlash(filepath.Join(scripts, "compose.sh")), "up", "-d")
		cmd.Env = append(os.Environ(), "OPS_START_LOG="+slash(log), "OPS_ROLE_EXIT="+failure)
		for i, value := range cmd.Env {
			if strings.HasPrefix(strings.ToUpper(value), "PATH=") {
				cmd.Env[i] = "PATH=" + slash(bin) + ":" + value[5:]
			}
		}
		output, err := cmd.CombinedOutput()
		data, readErr := os.ReadFile(log)
		if readErr != nil {
			t.Fatal(readErr)
		}
		lines := strings.Split(strings.TrimSpace(string(data)), "\n")
		if failure == "0" {
			if err != nil || len(lines) != 3 || !strings.Contains(lines[0], "--wait postgres") || !strings.Contains(lines[1], "ON_ERROR_STOP=1") || !strings.HasSuffix(lines[2], "up -d") {
				t.Fatalf("missing automatic roles/order: %v %s %s", err, output, data)
			}
		} else {
			if err == nil || len(lines) != 2 {
				t.Fatalf("role failure did not block platform: %v %s %s", err, output, data)
			}
		}
	}
}
