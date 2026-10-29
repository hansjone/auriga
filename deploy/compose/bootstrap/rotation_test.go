package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestRotationStrictInputAndNoAlternateCommand(t *testing.T) {
	for _, raw := range []string{`{"baseReceiptSha256":"a","unexpected":true}`, `{"privateFiles":{"key":"a","key":"b"}}`, `{} {}`, `{"BaseReceiptSha256":"a"}`} {
		var input rotationInput
		if rotationJSON([]byte(raw), &input) == nil {
			t.Fatalf("ambiguous/unknown input accepted: %s", raw)
		}
	}
	for _, args := range [][]string{{}, {"shell"}, {"publish", "a", "extra"}, {"lifecycle", "exec", "collab", "sh"}} {
		if err := keyRotationCLI(args); err == nil {
			t.Fatalf("unbounded CLI accepted: %v", args)
		}
	}
}

func TestRotationRejectsRecipeAndJavaEnvironmentOverrides(t *testing.T) {
	for _, key := range []string{"COMPOSE_PROJECT_NAME", "COMPOSE_FILE", "DOCKER_HOST", "LOADER_PATH", "LOADER_MAIN", "JAVA_TOOL_OPTIONS", "JDK_JAVA_OPTIONS", "_JAVA_OPTIONS", "CLASSPATH"} {
		t.Run(key, func(t *testing.T) {
			t.Setenv(key, "untrusted")
			if err := rotationEnvironment(); err == nil {
				t.Fatal("override accepted")
			}
		})
	}
}

func TestRotationVersionAndActiveGatewayProjection(t *testing.T) {
	for _, version := range []string{"0", "01", "-1", "../2", "9223372036854775808"} {
		raw, _ := json.Marshal(map[string]any{"keyset": map[string]any{"keyset_version": version}})
		if _, _, err := rotationPayload(raw); err == nil {
			t.Fatalf("unsafe version accepted: %s", version)
		}
	}
	now := time.Now().UTC().Truncate(time.Second)
	var keys []map[string]string
	for _, purpose := range []string{"business_delivery", "protocol_response", "management_state"} {
		keys = append(keys, map[string]string{"kid": purpose, "purpose": purpose, "state": "active", "not_before": now.Add(-time.Minute).Format(time.RFC3339), "not_after": now.Add(time.Hour).Format(time.RFC3339)})
	}
	if active, err := rotationActive(keys, now); err != nil || active["protocol_response"] != "protocol_response" {
		t.Fatalf("valid gateway wiring rejected: %v", err)
	}
	for _, probe := range []string{"next", "retired", "expired", "missing", "duplicate"} {
		t.Run(probe, func(t *testing.T) {
			copy := make([]map[string]string, len(keys))
			for i, key := range keys {
				copy[i] = map[string]string{}
				for k, v := range key {
					copy[i][k] = v
				}
			}
			switch probe {
			case "next", "retired":
				copy[1]["state"] = probe
			case "expired":
				copy[1]["not_after"] = now.Format(time.RFC3339)
			case "missing":
				copy = copy[:2]
			case "duplicate":
				copy = append(copy, copy[1])
			}
			if _, err := rotationActive(copy, now); err == nil {
				t.Fatal("unusable gateway key accepted")
			}
		})
	}
}

func TestRotationGatewayPreservesAllInheritedFieldsAndExactNumbers(t *testing.T) {
	source := []byte(`{"broker":{"url":"tls://broker.example.com:4222"},"budget":18446744073709551615,"protocolSigning":{"keyId":"old"},"iam":{"Target":"127.0.0.1:9091"}}`)
	entry := map[string]string{"kid": "new-protocol", "purpose": "protocol_response", "state": "active", "not_before": "2026-10-29T00:00:00Z", "not_after": "2026-11-29T00:00:00Z"}
	result, err := rotationGateway(source, "9", entry)
	if err != nil {
		t.Fatal(err)
	}
	var before, after map[string]json.RawMessage
	_ = json.Unmarshal(source, &before)
	_ = json.Unmarshal(result, &after)
	for _, key := range []string{"budget", "broker", "iam"} {
		if string(before[key]) != string(after[key]) {
			t.Fatalf("inherited field changed: %s", key)
		}
	}
	var protocol map[string]string
	_ = json.Unmarshal(after["protocolSigning"], &protocol)
	if protocol["keyId"] != "new-protocol" || protocol["keysetVersion"] != "9" || protocol["notBefore"] != entry["not_before"] || protocol["notAfter"] != entry["not_after"] || protocol["privateKeyFile"] != "/etc/auriga/network/protocol.key" {
		t.Fatal("protocol metadata lost binding")
	}
	for _, state := range []string{"next", "retired", "revoked"} {
		entry["state"] = state
		if _, err := rotationGateway(source, "9", entry); err == nil {
			t.Fatalf("gateway accepted %s", state)
		}
	}
	entry["state"] = "active"
	entry["purpose"] = "business_delivery"
	if _, err := rotationGateway(source, "9", entry); err == nil {
		t.Fatal("business key selected as protocol key")
	}
}

func rotationFake(t *testing.T) (*rotation, *[]string) {
	t.Helper()
	root := t.TempDir()
	r := &rotation{compose: root, out: filepath.Join(root, "var"), source: filepath.Join(root, "var", activationDirectory), state: filepath.Join(root, "original", "state.json"), config: filepath.Join(root, "original", "config"), network: filepath.Join(root, "original", "network"), project: "example-project"}
	var calls []string
	stopped := false
	var containers []rotationContainer
	args := r.composeArgs()
	var files []string
	for i, arg := range args {
		if arg == "-f" {
			files = append(files, args[i+1])
		}
	}
	for i, service := range []string{"platform", "edge", "network", "collab"} {
		var c rotationContainer
		c.ID = strings.Repeat(string(rune('a'+i)), 64)
		c.Image = "sha256:" + strings.Repeat("e", 64)
		c.State.Running, c.State.Pid = true, i+10
		c.State.StartedAt = "2026-10-28T00:00:00Z"
		c.Config.Labels = map[string]string{"com.docker.compose.service": service, "com.docker.compose.project": r.project, "com.docker.compose.project.working_dir": r.compose, "com.docker.compose.project.config_files": strings.Join(files, ",")}
		if service == "collab" {
			c.Mounts = append(c.Mounts, struct{ Source, Destination string }{r.config, "/etc/auriga/collab"}, struct{ Source, Destination string }{filepath.Dir(r.state), "/var/lib/auriga/collab"})
		}
		if service == "network" {
			c.Mounts = append(c.Mounts, struct{ Source, Destination string }{filepath.Join(r.network, "runtime.json"), "/etc/auriga/network/runtime.json"}, struct{ Source, Destination string }{filepath.Join(r.network, "protocol.key"), "/etc/auriga/network/protocol.key"})
		}
		containers = append(containers, c)
	}
	r.run = func(program string, args ...string) ([]byte, error) {
		calls = append(calls, program+" "+strings.Join(args, " "))
		if program != "/usr/bin/docker" {
			return nil, errors.New("unexpected executable")
		}
		if args[0] == "inspect" {
			copy := append([]rotationContainer{}, containers...)
			if stopped {
				for i := range copy {
					if copy[i].Config.Labels["com.docker.compose.service"] != "platform" {
						copy[i].State.Running = false
						copy[i].State.Pid = 0
						copy[i].State.FinishedAt = "2026-10-29T00:00:00Z"
					}
				}
			}
			return json.Marshal(copy)
		}
		if strings.Contains(strings.Join(args, " "), " stop ") {
			stopped = true
			return nil, nil
		}
		var ids []string
		for _, c := range containers {
			ids = append(ids, c.ID)
		}
		return []byte(strings.Join(ids, "\n")), nil
	}
	return r, &calls
}

func TestRotationStopBindsInstancesRecipeAndOriginalMount(t *testing.T) {
	r, calls := rotationFake(t)
	if err := r.stop(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	stops := 0
	for _, call := range *calls {
		if strings.Contains(call, " stop ") {
			stops++
			if strings.Contains(call, "stop platform") || !strings.Contains(call, "stop edge network collab") {
				t.Fatal("wrong services stopped")
			}
		}
		if strings.Contains(call, "down") || strings.Contains(call, "exec") || strings.Contains(call, " -v ") {
			t.Fatal("unbounded docker action")
		}
	}
	if stops != 1 {
		t.Fatal("missing bounded stop")
	}
	for _, probe := range []string{"project", "recipe", "state", "protocol", "instance", "still-running", "hidden-signer", "hidden-state-file", "hidden-ancestor", "missing"} {
		t.Run(probe, func(t *testing.T) {
			r, _ := rotationFake(t)
			original := r.run
			r.run = func(program string, args ...string) ([]byte, error) {
				raw, err := original(program, args...)
				if len(args) == 0 || args[0] != "inspect" || err != nil {
					return raw, err
				}
				var cs []rotationContainer
				_ = json.Unmarshal(raw, &cs)
				switch probe {
				case "project":
					cs[3].Config.Labels["com.docker.compose.project"] = "foreign"
				case "recipe":
					cs[3].Config.Labels["com.docker.compose.project.config_files"] = "foreign.yml"
				case "state":
					cs[3].Mounts[1].Source = filepath.Join(t.TempDir(), "different-state")
				case "protocol":
					cs[2].Mounts[0].Source = "foreign"
				case "instance":
					cs[3].ID = strings.Repeat("f", 64)
				case "still-running":
					cs[3].State.Running = true
				case "hidden-signer", "hidden-state-file", "hidden-ancestor":
					if cs[3].State.Pid == 0 {
						cs[0].Mounts = cs[3].Mounts
						if probe == "hidden-state-file" {
							cs[0].Mounts[1].Source = r.state
						}
						if probe == "hidden-ancestor" {
							cs[0].Mounts[1].Source = filepath.Dir(filepath.Dir(r.state))
						}
						cs[0].State.Running = true
						cs[0].State.Pid = 99
					}
				case "missing":
					cs = cs[:3]
				}
				return json.Marshal(cs)
			}
			if err := r.stop(t.TempDir()); err == nil {
				t.Fatal("unsafe stop proof accepted")
			}
		})
	}
}

func TestRotationInterruptedStateAndConcurrentOperationAreNeverRepaired(t *testing.T) {
	r, _ := rotationFake(t)
	if err := os.Mkdir(r.out, 0700); err != nil {
		t.Fatal(err)
	}
	failure := errors.New("synthetic failure after stop")
	err := r.mutate("publish", func(work string) error {
		if err := r.mutate("install", func(string) error { t.Fatal("concurrent body executed"); return nil }); err == nil {
			t.Fatal("concurrent mutation accepted")
		}
		return failure
	})
	if !errors.Is(err, failure) {
		t.Fatal(err)
	}
	path := filepath.Join(r.root(), ".pending", "first-failure.json")
	first, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(first), "incomplete") {
		t.Fatal("first failure not preserved")
	}
	if err := r.mutate("publish", func(string) error { return nil }); err == nil {
		t.Fatal("partial operation repaired")
	}
	again, _ := os.ReadFile(path)
	if string(first) != string(again) {
		t.Fatal("first failure overwritten")
	}
}

func TestRotationSuccessfulOperationOnlyRemovesItsOwnLock(t *testing.T) {
	r, _ := rotationFake(t)
	if err := os.Mkdir(r.out, 0700); err != nil {
		t.Fatal(err)
	}
	var replacement []byte
	err := r.mutate("fixture", func(work string) error {
		if err := rotationWrite(filepath.Join(work, "owned.json"), []byte(`{}`)); err != nil {
			return err
		}
		// Simulate replacement by another local owner; deferred cleanup must not unlink it.
		path := filepath.Join(r.root(), ".lock")
		if runtime.GOOS == "windows" {
			return nil
		}
		if err := os.Rename(path, path+".original"); err != nil {
			return err
		}
		replacement = []byte("different owner")
		return os.WriteFile(path, replacement, 0600)
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(replacement) > 0 {
		raw, err := os.ReadFile(filepath.Join(r.root(), ".lock"))
		if err != nil || string(raw) != string(replacement) {
			t.Fatal("another lock removed")
		}
	}
	if _, err := os.Stat(filepath.Join(r.root(), ".pending")); !os.IsNotExist(err) {
		t.Fatal("successful own stage retained")
	}
}

func TestRotationNeverDeletesAReplacedPendingDirectory(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux inode identity proof; Windows rotation is unavailable")
	}
	r, _ := rotationFake(t)
	if err := os.Mkdir(r.out, 0700); err != nil {
		t.Fatal(err)
	}
	err := r.mutate("fixture", func(work string) error {
		if err := os.Rename(work, work+".owned"); err != nil {
			return err
		}
		if err := os.Mkdir(work, 0700); err != nil {
			return err
		}
		return rotationWrite(filepath.Join(work, "foreign.json"), []byte(`{"owner":"another operation"}`))
	})
	if err == nil || !strings.Contains(err.Error(), "pending instance changed") {
		t.Fatalf("replacement not refused: %v", err)
	}
	if _, err := os.Stat(filepath.Join(r.root(), ".pending", "foreign.json")); err != nil {
		t.Fatal("another operation's pending directory removed")
	}
	if _, err := os.Stat(filepath.Join(r.root(), ".pending", "first-failure.json")); !os.IsNotExist(err) {
		t.Fatal("wrote failure evidence into another operation's directory")
	}
}

func TestRotationFixedOperatorMainAndPathOnlyArguments(t *testing.T) {
	r, _ := rotationFake(t)
	r.jar = filepath.Join(t.TempDir(), "platform.jar")
	plan := r.plan("signed-file", "announcement-file", "previous-file", "previous-announcement", "original-state", nil)
	// Fixed main and empty loader config precede the pinned classpath and path-only plan.
	r.run = func(program string, args ...string) ([]byte, error) {
		if program != "/usr/bin/java" || len(args) != 7 || args[0] != "-Dloader.main="+operatorMain || !strings.HasPrefix(args[1], "-Dloader.config.location=file:") || args[2] != "-cp" || args[3] != r.jar || args[4] != "org.springframework.boot.loader.launch.PropertiesLauncher" || args[5] != "verify" {
			t.Fatalf("untrusted launcher: %s %v", program, args)
		}
		return []byte(`{}`), nil
	}
	if _, err := r.operator("verify", t.TempDir(), plan); err != nil {
		t.Fatal(err)
	}
}

func TestRotationArchiveRejectsRollbackPathsMissingHistoryAndChangedBytes(t *testing.T) {
	r, _ := rotationFake(t)
	r.input = rotationInput{Base: strings.Repeat("a", 64), Identity: strings.Repeat("b", 64), Jar: strings.Repeat("c", 64)}
	r.sourceReceipt = strings.Repeat("d", 64)
	if err := os.MkdirAll(filepath.Join(r.source, "collab-installation"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := rotationWrite(filepath.Join(r.source, "collab-installation", "signed.json"), []byte(`{"keyset":{"keyset_version":"1"}}`)); err != nil {
		t.Fatal(err)
	}
	for _, v := range []string{"2", "3"} {
		dir := r.versionDir(v)
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		raw := []byte(`{"keyset":{"keyset_version":"` + v + `"}}`)
		if err := rotationWrite(filepath.Join(dir, "signed.json"), raw); err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"announcement.json", "pg-status.json"} {
			if err := rotationWrite(filepath.Join(dir, name), []byte(`{}`)); err != nil {
				t.Fatal(err)
			}
		}
		prev := "1"
		if v == "3" {
			prev = "2"
		}
		receipt := rotationReceipt{1, v, r.input.Base, r.input.Identity, r.sourceReceipt, r.input.Jar, prev, map[string]string{"signed.json": digestBytes(raw), "announcement.json": digestBytes([]byte(`{}`)), "pg-status.json": digestBytes([]byte(`{}`))}}
		if err := rotationWriteJSON(filepath.Join(dir, "publication.receipt"), receipt); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := r.archives(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(r.versionDir("2"), "signed.json"), []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := r.archives(); err == nil {
		t.Fatal("changed release accepted")
	}
	if err := rotationCheckFiles(t.TempDir(), map[string]string{"../source": strings.Repeat("a", 64)}); err == nil {
		t.Fatal("seal path traversal accepted")
	}
}
