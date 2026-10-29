package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

type rotationLinuxRecoveryFixture struct {
	r                        *rotation
	pg                       *rotationPGFixture
	controls                 []rotationContainer
	sourceInput              collabInput
	initial                  []byte
	pgVersion                string
	events                   []string
	statusError, verifyError error
}

func rotationLinuxRecovery(t *testing.T) *rotationLinuxRecoveryFixture {
	t.Helper()
	f := activationFixture(t)
	if err := runActivation(f.out, f.input, f.base, f.identity, false, false, true); err != nil {
		t.Fatal(err)
	}
	source, runtimeRoot := filepath.Join(f.out, activationDirectory), filepath.Join(f.out, "collab-activation-runtime")
	if raw, err := exec.Command("sh", "../scripts/collab-materials.sh", source, runtimeRoot).CombinedOutput(); err != nil {
		t.Fatalf("temporary fixture projection: %v %s", err, raw)
	}
	state := filepath.Join(runtimeRoot, "collab-runtime", "state", "state.json")
	if err := os.WriteFile(state+".lock", nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chown(state+".lock", 10002, 10002); err != nil {
		t.Fatal(err)
	}
	keys, _ := os.ReadFile(filepath.Join(f.out, "iam-installation", "compose.keys"))
	access, _ := os.ReadFile(filepath.Join(f.out, "access.env"))
	ca, _ := os.ReadFile(filepath.Join(f.out, "ca-sha256.txt"))
	env := append(append(append([]byte{}, keys...), []byte("AURIGA_AGENT_CA_SHA256="+strings.TrimSpace(string(ca))+"\n")...), access...)
	if err := os.WriteFile(filepath.Join(f.out, "compose.env"), env, 0600); err != nil {
		t.Fatal(err)
	}
	jar, jarBytes := filepath.Join(filepath.Dir(f.out), "fixture.jar"), []byte("injected executor fixture, never launched")
	if err := os.WriteFile(jar, jarBytes, 0600); err != nil {
		t.Fatal(err)
	}
	r := &rotation{compose: filepath.Dir(f.out), out: f.out, jar: jar, input: rotationInput{Base: f.base, Identity: f.identity, Jar: digestBytes(jarBytes)}}
	if err := r.binding(); err != nil {
		t.Fatal(err)
	}
	initial, _ := rotationRead(filepath.Join(source, "collab-installation", "signed.json"))
	version, _, err := rotationPayload(initial)
	if err != nil {
		t.Fatal(err)
	}
	input, _ := rotationRead(f.input)
	x := &rotationLinuxRecoveryFixture{r: r, initial: initial, pgVersion: version}
	if err := json.Unmarshal(input, &x.sourceInput); err != nil {
		t.Fatal(err)
	}
	x.pg = rotationPGFake(t, r)
	x.pg.container.State.Running, x.pg.container.State.Pid = true, 42
	x.createControls(false)
	r.run = func(program string, args ...string) ([]byte, error) {
		if program == "/usr/bin/docker" {
			if raw, err, handled := x.pg.execute(r, args); handled {
				x.events = append(x.events, "pg:"+x.pg.calls[len(x.pg.calls)-1])
				return raw, err
			}
			if args[0] == "inspect" {
				var result []rotationContainer
				for _, id := range args[3:] {
					for _, c := range x.controls {
						if c.ID == id {
							result = append(result, c)
						}
					}
					if x.pg.container != nil && x.pg.container.ID == id {
						result = append(result, *x.pg.container)
					}
				}
				return json.Marshal(result)
			}
			if args[0] == "ps" || strings.Contains(strings.Join(args, " "), " ps ") {
				var ids []string
				for _, c := range x.controls {
					ids = append(ids, c.ID)
				}
				if args[0] == "ps" && x.pg.container != nil {
					ids = append(ids, x.pg.container.ID)
				}
				return []byte(strings.Join(ids, "\n")), nil
			}
			command := args[len(r.composeArgs()):]
			x.events = append(x.events, "compose:"+strings.Join(command, " "))
			switch command[0] {
			case "stop":
				for i := range x.controls {
					if len(command) == 1 || x.controls[i].Config.Labels["com.docker.compose.service"] != "platform" {
						x.controls[i].State.Running, x.controls[i].State.Pid = false, 0
						x.controls[i].State.FinishedAt = "2026-10-29T00:00:00Z"
					}
				}
				if len(command) == 1 {
					x.pg.container.State.Running, x.pg.container.State.Pid = false, 0
				}
			case "down":
				x.controls, x.pg.container = nil, nil
			case "up", "start", "restart":
				if x.pg.container == nil || !x.pg.container.State.Running {
					return nil, fmt.Errorf("fixture signer start before PG recovery")
				}
				if len(x.controls) == 0 && command[0] != "up" {
					return nil, fmt.Errorf("fixture start requires existing containers")
				}
				x.createControls(true)
			default:
				return nil, fmt.Errorf("unexpected fixture Docker operation: %v", command)
			}
			return nil, nil
		}
		if program != "/usr/bin/java" {
			return nil, fmt.Errorf("unexpected fixture executable")
		}
		action := args[len(args)-2]
		x.events = append(x.events, "operator:"+action)
		var plan operatorPlan
		raw, err := rotationRead(args[len(args)-1])
		if err != nil || rotationJSON(raw, &plan) != nil {
			return nil, fmt.Errorf("fixture command plan refused")
		}
		if action == "verify" && x.verifyError != nil {
			return nil, x.verifyError
		}
		if action == "verify" || action == "check-private" {
			return []byte(`{}`), nil
		}
		if x.pg.container == nil || !x.pg.container.State.Running {
			return nil, fmt.Errorf("fixture PG offline")
		}
		if action == "status" && x.statusError != nil {
			return nil, x.statusError
		}
		signed, err := rotationRead(plan.Signed)
		if err != nil {
			return nil, err
		}
		version, entries, err := rotationPayload(signed)
		if err != nil {
			return nil, err
		}
		if action == "publish" {
			x.pgVersion = version
		} else if version != x.pgVersion {
			return nil, fmt.Errorf("fixture authoritative PG version differs")
		}
		if action == "install" {
			var body map[string]any
			state, err := os.ReadFile(r.state)
			if err != nil || json.Unmarshal(state, &body) != nil {
				return nil, fmt.Errorf("fixture state refused")
			}
			body["version"] = version
			state, _ = json.Marshal(body)
			if err := os.WriteFile(r.state, state, 0600); err != nil {
				return nil, err
			}
		}
		history := map[string]string{}
		for _, entry := range entries {
			history[entry["kid"]] = "2026-10-29T00:00:00Z"
		}
		return json.Marshal(map[string]any{"version": version, "databaseTime": time.Now().UTC().Format(time.RFC3339Nano), "manifestDigest": digestBytes(signed), "pendingPush": true, "firstPublished": history})
	}
	return x
}

func (x *rotationLinuxRecoveryFixture) createControls(installed bool) {
	r := x.r
	if len(x.controls) == 0 {
		var files []string
		args := r.composeArgs()
		for i, arg := range args {
			if arg == "-f" {
				files = append(files, args[i+1])
			}
		}
		for i, service := range []string{"platform", "edge", "network", "collab"} {
			c := rotationContainer{ID: strings.Repeat(string(rune('a'+i)), 64), Image: "sha256:" + strings.Repeat("e", 64)}
			c.Config.Labels = map[string]string{"com.docker.compose.service": service, "com.docker.compose.project": r.project, "com.docker.compose.project.working_dir": r.compose, "com.docker.compose.project.config_files": strings.Join(files, ",")}
			x.controls = append(x.controls, c)
		}
	}
	config, network := r.config, r.network
	if installed {
		config, network = filepath.Join(r.root(), "installs", x.pgVersion, "config"), filepath.Join(r.root(), "installs", x.pgVersion, "network")
	}
	for i := range x.controls {
		c := &x.controls[i]
		service := c.Config.Labels["com.docker.compose.service"]
		c.State.Running, c.State.Pid = true, i+10
		if service == "collab" || service == "network" {
			if installed && !strings.HasSuffix(c.Config.Labels["com.docker.compose.project.config_files"], ","+filepath.Join(r.root(), "current.yml")) {
				c.Config.Labels["com.docker.compose.project.config_files"] += "," + filepath.Join(r.root(), "current.yml")
			}
			c.Mounts = nil
			if service == "collab" {
				c.Mounts = append(c.Mounts, struct{ Source, Destination string }{config, "/etc/auriga/collab"}, struct{ Source, Destination string }{filepath.Dir(r.state), "/var/lib/auriga/collab"})
			} else {
				c.Mounts = append(c.Mounts, struct{ Source, Destination string }{filepath.Join(network, "runtime.json"), "/etc/auriga/network/runtime.json"}, struct{ Source, Destination string }{filepath.Join(network, "protocol.key"), "/etc/auriga/network/protocol.key"})
			}
		}
	}
}

func (x *rotationLinuxRecoveryFixture) round(t *testing.T, version string) {
	t.Helper()
	var signed map[string]any
	_ = json.Unmarshal(x.initial, &signed)
	signed["keyset"].(map[string]any)["keyset_version"] = version
	body, _ := json.Marshal(signed)
	dir := filepath.Dir(x.r.jar)
	x.r.input.SignedFile, x.r.input.AnnouncementFile = filepath.Join(dir, "candidate-"+version+".json"), filepath.Join(dir, "candidate-"+version+"-ann.json")
	for path, bytes := range map[string][]byte{x.r.input.SignedFile: body, x.r.input.AnnouncementFile: []byte(`{"fixture":"pair"}`)} {
		if err := rotationWrite(path, bytes); err != nil {
			t.Fatal(err)
		}
	}
	x.r.input.PrivateFiles = nil
	if err := x.r.publish(); err != nil {
		t.Fatalf("round %s publish: %v", version, err)
	}
	x.r.input.PrivateFiles = x.sourceInput.PrivateFiles
	if err := x.r.install(); err != nil {
		t.Fatalf("round %s install: %v", version, err)
	}
	if err := x.r.lifecycle([]string{"up", "-d"}); err != nil {
		t.Fatalf("round %s public lifecycle core: %v", version, err)
	}
}

func TestRotationLinuxConsecutiveRoundsKeepUnchangedPlatform(t *testing.T) {
	if !rotationSupported() {
		t.Skip("Linux installer-root fixture; no deployment")
	}
	x := rotationLinuxRecovery(t)
	platform := x.controls[0]
	lockBefore, err := os.Lstat(x.r.state + ".lock")
	if err != nil {
		t.Fatal(err)
	}
	version, _, _ := rotationPayload(x.initial)
	number, _ := strconv.Atoi(version)
	for _, n := range []int{number + 1, number + 2} {
		x.round(t, strconv.Itoa(n))
		if x.controls[0].ID != platform.ID || x.controls[0].Image != platform.Image || x.controls[0].Config.Labels["com.docker.compose.project.config_files"] != platform.Config.Labels["com.docker.compose.project.config_files"] {
			t.Fatal("unchanged platform replaced or relabeled")
		}
	}
	lockAfter, _ := os.Lstat(x.r.state + ".lock")
	if !os.SameFile(lockBefore, lockAfter) {
		t.Fatal("original state lock replaced")
	}
	receipts, err := x.r.archives()
	if err != nil || len(receipts) != 2 {
		t.Fatalf("two committed publications missing: %v", err)
	}
	if _, err := x.r.installed(receipts[1]); err != nil {
		t.Fatal(err)
	}
	if err := x.r.idle(); err != nil {
		t.Fatalf("second round left pending: %v", err)
	}
}

func TestRotationLinuxFullStopRecoveryAndAuthorityFence(t *testing.T) {
	if !rotationSupported() {
		t.Skip("Linux installer-root fixture; no deployment")
	}
	for _, probe := range []string{"stop-start", "stop-up", "stop-restart", "down-up", "cold-start", "retry-after-PG-error", "retry-after-start-failure", "PG-unreachable", "PG-start-failed", "wrong-project", "wrong-mount", "missing-volume", "wrong-history", "wrong-version", "wrong-signature", "pending", "state-changed"} {
		t.Run(probe, func(t *testing.T) {
			x := rotationLinuxRecovery(t)
			version, _, _ := rotationPayload(x.initial)
			n, _ := strconv.Atoi(version)
			x.round(t, strconv.Itoa(n+1))
			stop, start := "stop", "up"
			if probe == "down-up" {
				stop = "down"
			}
			if probe == "stop-start" || probe == "cold-start" {
				start = "start"
			}
			if probe == "stop-restart" {
				start = "restart"
			}
			if probe != "cold-start" {
				if err := x.r.lifecycle([]string{stop}); err != nil {
					t.Fatal(err)
				}
			} else {
				x.pg.container.State.Running, x.pg.container.State.Pid = false, 0
				for i := range x.controls {
					x.controls[i].State.Running, x.controls[i].State.Pid = false, 0
				}
			}
			x.events, x.pg.calls = nil, nil
			if probe == "retry-after-PG-error" || probe == "retry-after-start-failure" {
				if probe == "retry-after-PG-error" {
					x.statusError = fmt.Errorf("fixture temporary PG connection failure")
				} else {
					x.pg.startError = fmt.Errorf("fixture temporary PG startup failure")
				}
				if err := x.r.lifecycle([]string{start}); err == nil || strings.Contains(strings.Join(x.events, "|"), "compose:"+start) {
					t.Fatalf("interrupted dependency recovery started signers: %v %v", err, x.events)
				}
				if err := x.r.idle(); err != nil {
					t.Fatalf("dependency failure left a mutation pending: %v", err)
				}
				x.statusError, x.pg.startError = nil, nil
				x.events, x.pg.calls = nil, nil
			}
			switch probe {
			case "PG-unreachable":
				x.statusError = fmt.Errorf("fixture PG connection unavailable")
			case "PG-start-failed":
				x.pg.startError = fmt.Errorf("fixture PG failed health wait")
			case "wrong-project":
				x.pg.container.Config.Labels["com.docker.compose.project"] = "foreign"
			case "wrong-mount":
				x.pg.container.Mounts[0].Source = "foreign"
			case "missing-volume":
				x.pg.volume = nil
			case "wrong-history":
				x.statusError = fmt.Errorf("fixture first_published history changed")
			case "wrong-version":
				x.pgVersion = "1"
			case "wrong-signature":
				x.verifyError = fmt.Errorf("fixture root signature/state rejected")
			case "pending":
				if err := os.Mkdir(filepath.Join(x.r.root(), ".pending"), 0700); err != nil {
					t.Fatal(err)
				}
			case "state-changed":
				if err := os.WriteFile(x.r.state, []byte(`{}`), 0600); err != nil {
					t.Fatal(err)
				}
			}
			err := x.r.lifecycle([]string{start})
			calls := strings.Join(x.events, "|")
			positive := probe == "stop-start" || probe == "stop-up" || probe == "stop-restart" || probe == "down-up" || probe == "cold-start" || strings.HasPrefix(probe, "retry-after-")
			if positive {
				pg, status, verify, signers := strings.Index(calls, "pg:postgres-only-up"), strings.Index(calls, "operator:status"), strings.Index(calls, "operator:verify"), strings.Index(calls, "compose:"+start)
				if err != nil || pg < 0 || status <= pg || verify <= status || signers <= verify {
					t.Fatalf("recovery order/closure failed: %v %s", err, calls)
				}
			} else if err == nil || strings.Contains(calls, "compose:"+start) {
				t.Fatalf("signers crossed authority/installation fence: %v %s", err, calls)
			}
			if probe == "pending" || probe == "state-changed" {
				if len(x.pg.calls) != 0 {
					t.Fatal("dependency started before local installation/lock checks")
				}
			}
		})
	}
}

// This test uses only temporary files and an injected executor, never Docker/PG/WSL.
func TestRotationLinuxRootProjectionAndReadOnlyReentry(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("Linux root DAC projection; no real deployment")
	}
	f := activationFixture(t)
	if err := runActivation(f.out, f.input, f.base, f.identity, false, false, true); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(f.out, activationDirectory)
	runtimeRoot := filepath.Join(f.out, "collab-activation-runtime")
	if raw, err := exec.Command("sh", "../scripts/collab-materials.sh", source, runtimeRoot).CombinedOutput(); err != nil {
		t.Fatalf("temporary projection: %v %s", err, raw)
	}
	state := filepath.Join(runtimeRoot, "collab-runtime", "state", "state.json")
	if err := os.WriteFile(state+".lock", nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chown(state+".lock", 10002, 10002); err != nil {
		t.Fatal(err)
	}
	keys, _ := os.ReadFile(filepath.Join(f.out, "iam-installation", "compose.keys"))
	access, _ := os.ReadFile(filepath.Join(f.out, "access.env"))
	ca, _ := os.ReadFile(filepath.Join(f.out, "ca-sha256.txt"))
	composeEnv := append(append(append([]byte{}, keys...), []byte("AURIGA_AGENT_CA_SHA256="+strings.TrimSpace(string(ca))+"\n")...), access...)
	if err := os.WriteFile(filepath.Join(f.out, "compose.env"), composeEnv, 0600); err != nil {
		t.Fatal(err)
	}
	jar := filepath.Join(filepath.Dir(f.out), "fixture.jar")
	jarBytes := []byte("injected executor fixture; not a runnable Java artifact")
	if err := os.WriteFile(jar, jarBytes, 0600); err != nil {
		t.Fatal(err)
	}
	r := &rotation{compose: filepath.Dir(f.out), out: f.out, jar: jar, input: rotationInput{Base: f.base, Identity: f.identity, Jar: digestBytes(jarBytes)}}
	if err := r.binding(); err != nil {
		t.Fatal(err)
	}
	initial, _ := rotationRead(filepath.Join(source, "collab-installation", "signed.json"))
	var signed map[string]any
	_ = json.Unmarshal(initial, &signed)
	signed["keyset"].(map[string]any)["keyset_version"] = "8"
	candidate, _ := json.Marshal(signed)
	inputDir := filepath.Dir(f.input)
	r.input.SignedFile = filepath.Join(inputDir, "rotation-signed.json")
	r.input.AnnouncementFile = filepath.Join(inputDir, "rotation-announcement.json")
	if err := os.WriteFile(r.input.SignedFile, candidate, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(r.input.AnnouncementFile, []byte(`{"fixture":"pair"}`), 0600); err != nil {
		t.Fatal(err)
	}
	sealed := map[string]string{}
	for _, name := range activationFiles() {
		raw, err := rotationRead(filepath.Join(source, filepath.FromSlash(name)))
		if err != nil {
			t.Fatal(err)
		}
		sealed[name] = digestBytes(raw)
	}
	var instances []rotationContainer
	var recipe []string
	args := r.composeArgs()
	for i, arg := range args {
		if arg == "-f" {
			recipe = append(recipe, args[i+1])
		}
	}
	for i, service := range []string{"platform", "edge", "network", "collab"} {
		var c rotationContainer
		c.ID = strings.Repeat(string(rune('a'+i)), 64)
		c.Image = "sha256:" + strings.Repeat("e", 64)
		c.State.FinishedAt = "2026-10-29T00:00:00Z"
		c.Config.Labels = map[string]string{"com.docker.compose.service": service, "com.docker.compose.project": r.project, "com.docker.compose.project.working_dir": r.compose, "com.docker.compose.project.config_files": strings.Join(recipe, ",")}
		if service == "collab" {
			c.Mounts = append(c.Mounts, struct{ Source, Destination string }{r.config, "/etc/auriga/collab"}, struct{ Source, Destination string }{filepath.Dir(r.state), "/var/lib/auriga/collab"})
		}
		if service == "network" {
			c.Mounts = append(c.Mounts, struct{ Source, Destination string }{filepath.Join(r.network, "runtime.json"), "/etc/auriga/network/runtime.json"}, struct{ Source, Destination string }{filepath.Join(r.network, "protocol.key"), "/etc/auriga/network/protocol.key"})
		}
		instances = append(instances, c)
	}
	javaMutations := 0
	r.run = func(program string, args ...string) ([]byte, error) {
		if program == "/usr/bin/docker" {
			if args[0] == "inspect" {
				return json.Marshal(instances)
			}
			if strings.Contains(strings.Join(args, " "), " stop ") {
				return nil, nil
			}
			var ids []string
			for _, c := range instances {
				ids = append(ids, c.ID)
			}
			return []byte(strings.Join(ids, "\n")), nil
		}
		if program != "/usr/bin/java" {
			return nil, fmt.Errorf("unexpected executor")
		}
		action := args[len(args)-2]
		var plan operatorPlan
		raw, err := rotationRead(args[len(args)-1])
		if err != nil {
			return nil, err
		}
		if err := rotationJSON(raw, &plan); err != nil {
			return nil, err
		}
		if action == "publish" {
			javaMutations++
		}
		if action == "install" {
			javaMutations++
			var stateBody map[string]any
			old, err := os.ReadFile(r.state)
			if err != nil {
				return nil, err
			}
			_ = json.Unmarshal(old, &stateBody)
			stateBody["version"] = "8"
			body, _ := json.Marshal(stateBody)
			if err := os.WriteFile(r.state, body, 0600); err != nil {
				return nil, err
			}
		}
		if action == "status" || action == "publish" || action == "install" {
			_, entries, _ := rotationPayload(candidate)
			history := map[string]string{}
			for _, entry := range entries {
				history[entry["kid"]] = time.Now().UTC().Format(time.RFC3339Nano)
			}
			return json.Marshal(map[string]any{"version": "8", "databaseTime": time.Now().UTC().Format(time.RFC3339Nano), "manifestDigest": "fixture", "pendingPush": true, "firstPublished": history})
		}
		return []byte(`{}`), nil
	}
	if err := r.publish(); err != nil {
		t.Fatal(err)
	}
	if err := r.publish(); err != nil {
		t.Fatalf("same-byte publication reentry: %v", err)
	}
	var originalInput collabInput
	inputBytes, _ := rotationRead(f.input)
	_ = json.Unmarshal(inputBytes, &originalInput)
	r.input.PrivateFiles = originalInput.PrivateFiles
	if err := r.install(); err != nil {
		t.Fatal(err)
	}
	if err := r.install(); err != nil {
		t.Fatalf("same-byte installation reentry: %v", err)
	}
	if javaMutations != 2 {
		t.Fatalf("reentry performed mutations: %d", javaMutations)
	}
	for name, hash := range sealed {
		raw, err := rotationRead(filepath.Join(source, filepath.FromSlash(name)))
		if err != nil || digestBytes(raw) != hash {
			t.Fatalf("original source changed: %s %v", name, err)
		}
	}
	if _, err := checkActivation(f.out); err != nil {
		t.Fatalf("original seal no longer valid: %v", err)
	}
	if err := os.Chmod(filepath.Join(r.root(), "installs", "8", "network", "protocol.key"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := r.install(); err == nil {
		t.Fatal("exposed runtime private accepted on reentry")
	}
}

func TestRotationLinuxRejectsSourceLinksAndWritableAncestors(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("Linux root source metadata fixture")
	}
	for _, probe := range []string{"hardlink", "symlink", "parent-symlink", "parent-writable", "private-readable"} {
		t.Run(probe, func(t *testing.T) {
			dir := t.TempDir()
			private := filepath.Join(dir, "private.der")
			if err := os.WriteFile(private, []byte("test-only secret bytes"), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := rotationRead(private); err != nil {
				t.Fatal(err)
			}
			switch probe {
			case "hardlink":
				if err := os.Link(private, filepath.Join(dir, "linked.der")); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				link := filepath.Join(dir, "symlink.der")
				if err := os.Symlink(private, link); err != nil {
					t.Fatal(err)
				}
				private = link
			case "parent-symlink":
				link := dir + "-linked"
				if err := os.Symlink(dir, link); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = os.Remove(link) })
				private = filepath.Join(link, "private.der")
			case "parent-writable":
				if err := os.Chmod(dir, 0777); err != nil {
					t.Fatal(err)
				}
			case "private-readable":
				if err := os.Chmod(private, 0644); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := rotationRead(private); err == nil {
				t.Fatal("unsafe source accepted")
			}
		})
	}
}

func TestRotationLinuxSystemAncestorsDoNotGrantOperations(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "input.json")
	if err := os.WriteFile(input, []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}
	t.Logf("fixture uid=%d euid=%d supported=%t", os.Getuid(), os.Geteuid(), rotationSupported())
	// A setuid context is only a gate test: its created leaf has the effective
	// owner's identity, so it must not stand in for a caller-owned fixture.
	if os.Getuid() == os.Geteuid() {
		t.Run("protected-caller-leaf-under-system-ancestors", func(t *testing.T) {
			if raw, err := rotationRead(input); err != nil || string(raw) != `{}` {
				t.Fatalf("protected fixture refused: %v", err)
			}
		})
	}
	t.Run("writable-ancestor-rejected", func(t *testing.T) {
		parent := filepath.Join(dir, "writable")
		if err := os.Mkdir(parent, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(parent, 0777); err != nil {
			t.Fatal(err)
		}
		if err := rotationInputParents(filepath.Join(parent, "input.json")); err == nil || !strings.Contains(err.Error(), "ancestor DAC refused") {
			t.Fatalf("writable ancestor accepted: %v", err)
		}
	})
	t.Run("symlink-ancestor-rejected", func(t *testing.T) {
		link := filepath.Join(dir, "linked")
		if err := os.Symlink(dir, link); err != nil {
			t.Fatal(err)
		}
		if err := rotationInputParents(filepath.Join(link, "input.json")); err == nil {
			t.Fatal("symlink ancestor accepted")
		}
	})
	if os.Getuid() != 0 || os.Geteuid() != 0 {
		t.Run("public-cli-still-requires-real-and-effective-root", func(t *testing.T) {
			for _, entry := range os.Environ() {
				key, _, _ := strings.Cut(entry, "=")
				if strings.HasPrefix(key, "COMPOSE_") || strings.HasPrefix(key, "DOCKER_") || strings.HasPrefix(key, "LOADER_") || key == "JAVA_TOOL_OPTIONS" || key == "JDK_JAVA_OPTIONS" || key == "_JAVA_OPTIONS" || key == "CLASSPATH" {
					t.Setenv(key, "")
				}
			}
			err := keyRotationCLI([]string{"status", input})
			if err == nil || !strings.Contains(err.Error(), "requires Linux installer root") {
				t.Fatalf("unprivileged or setuid operation passed the root gate: %v", err)
			}
		})
	}
}
