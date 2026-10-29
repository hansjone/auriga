package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type rotationContainer struct {
	ID    string `json:"Id"`
	Image string
	State struct {
		Running, Paused, Restarting bool
		Pid                         int
		StartedAt, FinishedAt       string
	}
	Config struct {
		Labels map[string]string
		Image  string
	}
	Mounts []struct{ Source, Destination string }
}

type rotationInstallReceipt struct {
	Binding     rotationReceipt `json:"binding"`
	Publication string          `json:"publicationReceiptSha256"`
	State       string          `json:"stateSha256"`
}

type rotationCurrent struct {
	Version string `json:"version"`
	Receipt string `json:"installReceiptSha256"`
}

func (r *rotation) composeArgs() []string {
	args := []string{"compose", "--project-name", r.project, "--project-directory", r.compose, "--env-file", filepath.Join(r.out, "compose.env"), "-f", filepath.Join(r.compose, "docker-compose.yml"), "--profile", "network-collab"}
	overlay := "collab-required.yml"
	if r.source != r.out {
		overlay = "collab-activation.yml"
	}
	args = append(args, "-f", filepath.Join(r.compose, "scripts", overlay))
	if _, err := os.Lstat(filepath.Join(r.root(), "current.json")); err == nil {
		args = append(args, "-f", filepath.Join(r.root(), "current.yml"))
	}
	return args
}

func (r *rotation) containers(ids []string) ([]rotationContainer, error) {
	if len(ids) == 0 || len(ids) > 1024 {
		return nil, fmt.Errorf("rotation container inventory refused")
	}
	for _, id := range ids {
		if len(id) != 64 || !rotationHash.MatchString(id) {
			return nil, fmt.Errorf("rotation container identifier refused")
		}
	}
	raw, err := r.run("/usr/bin/docker", append([]string{"inspect", "--type", "container"}, ids...)...)
	var containers []rotationContainer
	if err != nil || json.Unmarshal(raw, &containers) != nil || len(containers) != len(ids) {
		return nil, fmt.Errorf("rotation container inspection refused")
	}
	seen := map[string]bool{}
	for _, c := range containers {
		if seen[c.ID] || !containsRotationID(ids, c.ID) {
			return nil, fmt.Errorf("rotation container instance changed")
		}
		seen[c.ID] = true
	}
	return containers, nil
}

func containsRotationID(ids []string, id string) bool {
	for _, candidate := range ids {
		if candidate == id {
			return true
		}
	}
	return false
}

func (r *rotation) checkContainer(c rotationContainer) error {
	service := c.Config.Labels["com.docker.compose.service"]
	if c.Config.Labels["com.docker.compose.project"] != r.project || c.Config.Labels["com.docker.compose.project.working_dir"] != r.compose || (service != "collab" && service != "network" && service != "platform" && service != "edge") || c.Image == "" {
		return fmt.Errorf("rotation foreign container/project refused")
	}
	if err := r.checkRecipe(c); err != nil {
		return err
	}
	mounts := map[string]string{}
	for _, mount := range c.Mounts {
		if _, exists := mounts[mount.Destination]; exists {
			return fmt.Errorf("rotation ambiguous container mount")
		}
		mounts[mount.Destination] = filepath.Clean(mount.Source)
	}
	config, network := r.config, r.network
	if raw, err := rotationRead(filepath.Join(r.root(), "current.json")); err == nil {
		var current rotationCurrent
		if rotationJSON(raw, &current) != nil || !rotationHash.MatchString(current.Receipt) {
			return fmt.Errorf("rotation installed pointer invalid")
		}
		config = filepath.Join(r.root(), "installs", current.Version, "config")
		network = filepath.Join(r.root(), "installs", current.Version, "network")
	}
	if service == "collab" && (mounts["/etc/auriga/collab"] != config || mounts["/var/lib/auriga/collab"] != filepath.Dir(r.state)) {
		return fmt.Errorf("rotation signer mount differs from original state/config")
	}
	if service == "network" {
		if mounts["/etc/auriga/network/runtime.json"] != filepath.Join(network, "runtime.json") || mounts["/etc/auriga/network/protocol.key"] != filepath.Join(network, "protocol.key") {
			if network != filepath.Join(r.out, "network") || mounts["/etc/auriga/network"] != network {
				return fmt.Errorf("rotation gateway protocol mount differs")
			}
		}
	}
	return nil
}

func (r *rotation) checkRecipe(c rotationContainer) error {
	args := r.composeArgs()
	var files []string
	for i, arg := range args {
		if arg == "-f" {
			files = append(files, args[i+1])
		}
	}
	created := c.Config.Labels["com.docker.compose.project.config_files"]
	service := c.Config.Labels["com.docker.compose.service"]
	// The operations overlay only changes collab/network. Compose can retain
	// unchanged services with their exact original two-file creation labels.
	unchanged := service == "platform" || service == "edge" || service == "postgres"
	if created == strings.Join(files, ",") || (unchanged && len(files) == 3 && created == strings.Join(files[:2], ",")) {
		return nil
	}
	return fmt.Errorf("rotation running recipe differs")
}

func (r *rotation) stop(work string) error {
	args := r.composeArgs()
	raw, err := r.run("/usr/bin/docker", append(args, "ps", "--all", "-q", "platform", "edge", "network", "collab")...)
	if err != nil {
		return err
	}
	ids := strings.Fields(string(raw))
	before, err := r.containers(ids)
	if err != nil {
		return err
	}
	services := map[string]bool{}
	for _, c := range before {
		if err := r.checkContainer(c); err != nil {
			return err
		}
		service := c.Config.Labels["com.docker.compose.service"]
		if services[service] {
			return fmt.Errorf("rotation multiple signer/control replicas refused")
		}
		services[service] = true
	}
	if len(services) != 4 {
		return fmt.Errorf("rotation requires existing four control service instances")
	}
	if _, err := r.run("/usr/bin/docker", append(args, "stop", "edge", "network", "collab")...); err != nil {
		return err
	}
	after, err := r.containers(ids)
	if err != nil {
		return err
	}
	for _, c := range after {
		if err := r.checkContainer(c); err != nil {
			return err
		}
		if c.Config.Labels["com.docker.compose.service"] != "platform" && (c.State.Running || c.State.Paused || c.State.Restarting || c.State.Pid != 0 || c.State.FinishedAt == "" || strings.HasPrefix(c.State.FinishedAt, "0001-")) {
			return fmt.Errorf("rotation control/signer container has not stopped")
		}
		for _, old := range before {
			if old.ID == c.ID && old.Image != c.Image {
				return fmt.Errorf("rotation stopped instance image changed")
			}
		}
	}
	// A second project/name must not hide another live signer on the same writable mount.
	allIDs, err := r.run("/usr/bin/docker", "ps", "--all", "--quiet", "--no-trunc")
	if err != nil {
		return err
	}
	all, err := r.containers(strings.Fields(string(allIDs)))
	if err != nil {
		return err
	}
	for _, c := range all {
		for _, mount := range c.Mounts {
			rel, err := filepath.Rel(filepath.Clean(mount.Source), r.state)
			touches := err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
			if touches && (c.State.Running || c.State.Paused || c.State.Restarting || c.State.Pid != 0) {
				return fmt.Errorf("rotation another live process owns original signer state")
			}
		}
	}
	return rotationWriteJSON(filepath.Join(work, "stopped-instances.json"), after)
}

func (r *rotation) install() error {
	if err := r.idle(); err != nil {
		return err
	}
	if err := r.liveCheckpoint(); err != nil {
		return err
	}
	receipts, err := r.archives()
	if err != nil || len(receipts) == 0 {
		return fmt.Errorf("rotation requires a committed public publication")
	}
	last := receipts[len(receipts)-1]
	signed, err := rotationRead(r.input.SignedFile)
	if err != nil {
		return err
	}
	ann, err := rotationRead(r.input.AnnouncementFile)
	if err != nil || last.Files["signed.json"] != digestBytes(signed) || last.Files["announcement.json"] != digestBytes(ann) {
		return fmt.Errorf("rotation installation must match exact latest publication bytes")
	}
	version, keys, err := rotationPayload(signed)
	if err != nil {
		return err
	}
	if _, err := rotationActive(keys, time.Now()); err != nil {
		return err
	}
	required := 0
	for _, key := range keys {
		if key["state"] == "active" || key["state"] == "next" {
			required++
			if !collabKid.MatchString(key["kid"]) || r.input.PrivateFiles[key["kid"]] == "" {
				return fmt.Errorf("rotation every active/next private input required")
			}
		}
	}
	if required != len(r.input.PrivateFiles) {
		return fmt.Errorf("rotation exact active/next private input set required")
	}
	if _, err := os.Lstat(filepath.Join(r.root(), "installs", version)); err == nil {
		current, err := r.installed(last)
		if err != nil || current.Version != version {
			return fmt.Errorf("rotation previous installation is incomplete or changed")
		}
		for kid, path := range r.input.PrivateFiles {
			raw, err := rotationRead(path)
			if err != nil || last.Version == "" {
				return fmt.Errorf("rotation reentry private input refused")
			}
			installed, err := rotationRuntimeRead(filepath.Join(r.root(), "installs", version, "config", "keys", kid+".der"), 10002)
			if err != nil || !bytes.Equal(raw, installed) {
				clear(raw)
				clear(installed)
				return fmt.Errorf("rotation reentry private bytes changed")
			}
			clear(raw)
			clear(installed)
		}
		return r.readStatus(r.versionDir(version)+string(filepath.Separator)+"signed.json", filepath.Join(r.versionDir(version), "announcement.json"))
	}
	return r.mutate("install", func(work string) error {
		stage := filepath.Join(work, "runtime")
		for _, dir := range []string{stage, filepath.Join(stage, "config"), filepath.Join(stage, "config", "keys"), filepath.Join(stage, "network")} {
			if err := os.Mkdir(dir, 0700); err != nil {
				return err
			}
		}
		config := filepath.Join(stage, "config")
		for _, name := range []string{"root.der", "browser.crt", "ca.crt", "server.crt", "server.key", "client.crt", "client.key"} {
			raw, err := rotationRead(filepath.Join(r.source, "collab-installation", name))
			if err != nil {
				return err
			}
			if err := rotationWrite(filepath.Join(config, name), raw); err != nil {
				clear(raw)
				return err
			}
			clear(raw)
		}
		for name, raw := range map[string][]byte{"signed.json": signed, "announcement.json": ann} {
			if err := rotationWrite(filepath.Join(config, name), raw); err != nil {
				return err
			}
		}
		secrets := map[string]string{}
		var protocol []byte
		defer func() { clear(protocol) }()
		var protocolEntry map[string]string
		for _, key := range keys {
			if key["state"] != "active" && key["state"] != "next" {
				continue
			}
			kid := key["kid"]
			if !collabKid.MatchString(kid) || r.input.PrivateFiles[kid] == "" {
				return fmt.Errorf("rotation requires every active/next private key")
			}
			raw, err := rotationRead(r.input.PrivateFiles[kid])
			if err != nil {
				return err
			}
			path := filepath.Join(config, "keys", kid+".der")
			if err := rotationWrite(path, raw); err != nil {
				clear(raw)
				return err
			}
			secrets[kid] = path
			if key["purpose"] == "protocol_response" && key["state"] == "active" {
				protocol = append([]byte{}, raw...)
				protocolEntry = key
			}
			clear(raw)
		}
		if len(secrets) != len(r.input.PrivateFiles) {
			return fmt.Errorf("rotation unlisted private key refused")
		}
		properties, err := rotationRead(filepath.Join(r.source, "collab-installation", "collab.properties"))
		if err != nil {
			return err
		}
		defer clear(properties)
		var lines []string
		for _, line := range strings.Split(string(properties), "\n") {
			if !strings.HasPrefix(line, "auriga.collab.network.key-operations.private-files[") {
				lines = append(lines, line)
			}
		}
		for _, key := range keys {
			if _, ok := secrets[key["kid"]]; ok {
				lines = append(lines, "auriga.collab.network.key-operations.private-files["+key["kid"]+"]=/etc/auriga/collab/keys/"+key["kid"]+".der")
			}
		}
		if err := rotationWrite(filepath.Join(config, "collab.properties"), []byte(strings.Join(lines, "\n")+"\n")); err != nil {
			return err
		}
		gateway, err := collabRead(filepath.Join(r.source, "network", "runtime.json"), 1<<20, r.source != r.out)
		if err != nil || protocolEntry == nil {
			return fmt.Errorf("rotation gateway source refused")
		}
		runtime, err := rotationGateway(gateway, version, protocolEntry)
		if err != nil {
			return err
		}
		if err := rotationWrite(filepath.Join(stage, "network", "runtime.json"), runtime); err != nil {
			return err
		}
		if err := rotationWrite(filepath.Join(stage, "network", "protocol.key"), rotationPEM(protocol)); err != nil {
			return err
		}
		plan := r.plan(filepath.Join(config, "signed.json"), filepath.Join(config, "announcement.json"), filepath.Join(r.versionDir(version), "signed.json"), filepath.Join(r.versionDir(version), "announcement.json"), filepath.Join(work, "checked-state.json"), secrets)
		if _, err := r.operator("check-private", work, plan); err != nil {
			return err
		}
		if err := r.stop(work); err != nil {
			return err
		}
		plan.State = r.state
		if _, err := r.operator("install", work, plan); err != nil {
			return err
		}
		if err := rotationOwnState(r.state); err != nil {
			return err
		}
		state, err := rotationRuntimeRead(r.state, 10002)
		if err != nil {
			return err
		}
		files := map[string]string{}
		if err := filepath.WalkDir(stage, func(path string, entry fs.DirEntry, err error) error {
			if err != nil || entry.IsDir() {
				return err
			}
			raw, err := rotationRead(path)
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(stage, path)
			files[filepath.ToSlash(rel)] = digestBytes(raw)
			clear(raw)
			return nil
		}); err != nil {
			return err
		}
		publication, err := rotationRead(filepath.Join(r.versionDir(version), "publication.receipt"))
		if err != nil {
			return err
		}
		binding := last
		binding.Files = files
		installed := rotationInstallReceipt{binding, digestBytes(publication), digestBytes(state)}
		if err := rotationWriteJSON(filepath.Join(stage, "install.receipt"), installed); err != nil {
			return err
		}
		if err := rotationOwnRuntime(stage, r.state); err != nil {
			return err
		}
		if err := r.binding(); err != nil {
			return err
		}
		destination := filepath.Join(r.root(), "installs", version)
		if err := os.Rename(stage, destination); err != nil {
			return err
		}
		installReceipt, err := rotationRead(filepath.Join(destination, "install.receipt"))
		if err != nil {
			return err
		}
		if err := rotationWriteJSON(filepath.Join(work, "current.json"), rotationCurrent{version, digestBytes(installReceipt)}); err != nil {
			return err
		}
		if err := rotationWrite(filepath.Join(work, "current.yml"), r.overlay(version)); err != nil {
			return err
		}
		// Starts are blocked by .pending until both fixed selector files have committed.
		for _, name := range []string{"current.yml", "current.json"} {
			if err := os.Rename(filepath.Join(work, name), filepath.Join(r.root(), name)); err != nil {
				return fmt.Errorf("rotation selector half-commit; retain pending evidence")
			}
		}
		return nil
	})
}

func rotationGateway(source []byte, version string, entry map[string]string) ([]byte, error) {
	var original map[string]json.RawMessage
	if entry == nil || entry["purpose"] != "protocol_response" || entry["state"] != "active" || !collabKid.MatchString(entry["kid"]) || json.Unmarshal(source, &original) != nil || original == nil {
		return nil, fmt.Errorf("rotation active protocol binding refused")
	}
	protocol, err := json.Marshal(map[string]string{"keyId": entry["kid"], "keysetVersion": version, "notBefore": entry["not_before"], "notAfter": entry["not_after"], "privateKeyFile": "/etc/auriga/network/protocol.key"})
	if err != nil {
		return nil, err
	}
	original["protocolSigning"] = protocol
	return json.Marshal(original)
}

func (r *rotation) overlay(version string) []byte {
	// Numeric version and fixed relative paths only. This is not caller-supplied Compose YAML.
	root := "./var/collab-operations/installs/" + version
	return []byte("services:\n  collab:\n    volumes:\n      - " + root + "/config:/etc/auriga/collab:ro\n  network:\n    volumes:\n      - " + root + "/network/runtime.json:/etc/auriga/network/runtime.json:ro\n      - " + root + "/network/protocol.key:/etc/auriga/network/protocol.key:ro\n")
}

func (r *rotation) installed(last rotationReceipt) (*rotationCurrent, error) {
	raw, err := rotationRead(filepath.Join(r.root(), "current.json"))
	var current rotationCurrent
	if err != nil || rotationJSON(raw, &current) != nil || current.Version != last.Version || !rotationHash.MatchString(current.Receipt) {
		return nil, fmt.Errorf("rotation PG publication awaiting same-version installation; start refused")
	}
	dir := filepath.Join(r.root(), "installs", current.Version)
	for _, name := range []string{"config", "config/keys", "network"} {
		uid := 10002
		if name == "network" {
			uid = 10001
		}
		if !rotationRuntimeDirectory(filepath.Join(dir, filepath.FromSlash(name)), uid) {
			return nil, fmt.Errorf("rotation mounted directory DAC refused")
		}
	}
	raw, err = rotationRead(filepath.Join(dir, "install.receipt"))
	var receipt rotationInstallReceipt
	if err != nil || digestBytes(raw) != current.Receipt || rotationJSON(raw, &receipt) != nil || receipt.Binding.Version != last.Version || receipt.Binding.Base != last.Base || receipt.Binding.Identity != last.Identity || receipt.Binding.Source != last.Source || receipt.Binding.Jar != last.Jar {
		return nil, fmt.Errorf("rotation installed receipt changed")
	}
	publication, err := rotationRead(filepath.Join(r.versionDir(last.Version), "publication.receipt"))
	if err != nil || digestBytes(publication) != receipt.Publication {
		return nil, fmt.Errorf("rotation installation publication binding changed")
	}
	for name, hash := range receipt.Binding.Files {
		if !fs.ValidPath(name) || !rotationHash.MatchString(hash) || (!strings.HasPrefix(name, "config/") && !strings.HasPrefix(name, "network/")) {
			return nil, fmt.Errorf("rotation installed path refused")
		}
		uid := 10002
		if strings.HasPrefix(name, "network/") {
			uid = 10001
		}
		raw, err := rotationRuntimeRead(filepath.Join(dir, filepath.FromSlash(name)), uid)
		if err != nil || digestBytes(raw) != hash {
			clear(raw)
			return nil, fmt.Errorf("rotation runtime bytes or DAC changed")
		}
		clear(raw)
	}
	state, err := rotationRuntimeRead(r.state, 10002)
	if err != nil || digestBytes(state) != receipt.State {
		return nil, fmt.Errorf("rotation original runtime state changed")
	}
	if err := rotationStateLock(r.state); err != nil {
		return nil, err
	}
	overlay, err := rotationRead(filepath.Join(r.root(), "current.yml"))
	if err != nil || !bytes.Equal(overlay, r.overlay(current.Version)) {
		return nil, fmt.Errorf("rotation fixed overlay changed")
	}
	return &current, nil
}

func (r *rotation) liveCheckpoint() error {
	if !rotationRuntimeDirectory(filepath.Dir(r.state), 10002) {
		return fmt.Errorf("rotation original state directory DAC refused")
	}
	if err := rotationStateLock(r.state); err != nil {
		return err
	}
	if raw, err := rotationRead(filepath.Join(r.root(), "current.json")); err == nil {
		var current rotationCurrent
		if rotationJSON(raw, &current) != nil {
			return fmt.Errorf("rotation installed pointer refused")
		}
		receipts, err := r.archives()
		if err != nil {
			return err
		}
		for _, receipt := range receipts {
			if receipt.Version == current.Version {
				_, err := r.installed(receipt)
				return err
			}
		}
		return fmt.Errorf("rotation installed history missing")
	}
	for _, name := range collabLeaves {
		source, err := rotationRead(filepath.Join(r.source, "collab-installation", name))
		if err != nil {
			return err
		}
		path := filepath.Join(r.config, name)
		if name == "state.json" {
			path = r.state
		}
		actual, err := rotationRuntimeRead(path, 10002)
		if err != nil || !bytes.Equal(source, actual) {
			clear(source)
			clear(actual)
			return fmt.Errorf("rotation original runtime differs from sealed source")
		}
		clear(source)
		clear(actual)
	}
	return nil
}

type rotationPGVolume struct {
	Name, Driver, Mountpoint, Scope, CreatedAt string
	Labels, Options                            map[string]string
}

func (r *rotation) pgVolume() (rotationPGVolume, error) {
	name := r.project + "_pgdata"
	raw, err := r.run("/usr/bin/docker", "volume", "inspect", name)
	var volumes []rotationPGVolume
	if err != nil || json.Unmarshal(raw, &volumes) != nil || len(volumes) != 1 {
		return rotationPGVolume{}, fmt.Errorf("rotation existing PostgreSQL volume required")
	}
	v := volumes[0]
	if v.Name != name || v.Driver != "local" || v.Scope != "local" || v.CreatedAt == "" || !filepath.IsAbs(v.Mountpoint) || filepath.Clean(v.Mountpoint) != v.Mountpoint || len(v.Options) != 0 || v.Labels["com.docker.compose.project"] != r.project || v.Labels["com.docker.compose.volume"] != "pgdata" {
		return rotationPGVolume{}, fmt.Errorf("rotation PostgreSQL volume binding refused")
	}
	return v, nil
}

func (r *rotation) pgContainer(volume rotationPGVolume) (*rotationContainer, error) {
	raw, err := r.run("/usr/bin/docker", append(r.composeArgs(), "ps", "--all", "-q", "postgres")...)
	if err != nil {
		return nil, err
	}
	ids := strings.Fields(string(raw))
	if len(ids) == 0 {
		return nil, nil
	}
	if len(ids) != 1 {
		return nil, fmt.Errorf("rotation PostgreSQL replicas refused")
	}
	containers, err := r.containers(ids)
	if err != nil {
		return nil, err
	}
	c := containers[0]
	if c.Config.Labels["com.docker.compose.service"] != "postgres" || c.Config.Labels["com.docker.compose.project"] != r.project || c.Config.Labels["com.docker.compose.project.working_dir"] != r.compose || c.Config.Image != "postgres:17" || !strings.HasPrefix(c.Image, "sha256:") || !rotationHash.MatchString(strings.TrimPrefix(c.Image, "sha256:")) || c.State.Paused || c.State.Restarting {
		return nil, fmt.Errorf("rotation PostgreSQL container binding refused")
	}
	if err := r.checkRecipe(c); err != nil {
		return nil, err
	}
	expected := map[string]string{
		"/var/lib/postgresql/data":                        volume.Mountpoint,
		"/docker-entrypoint-initdb.d/01-roles.sql":        filepath.Join(r.compose, "postgres", "init.sql"),
		"/docker-entrypoint-initdb.d/02-collab-roles.sql": filepath.Join(r.compose, "scripts", "collab-roles.sql"),
	}
	if len(c.Mounts) != len(expected) {
		return nil, fmt.Errorf("rotation PostgreSQL mounts refused")
	}
	for _, mount := range c.Mounts {
		if source, exists := expected[mount.Destination]; !exists || source != mount.Source {
			return nil, fmt.Errorf("rotation PostgreSQL mounts refused")
		}
		delete(expected, mount.Destination)
	}
	return &c, nil
}

func (r *rotation) resumePostgres() error {
	// Only this dependency may start before the authoritative PG checks. Never
	// create a replacement volume or recreate a retained database container.
	volume, err := r.pgVolume()
	if err != nil {
		return err
	}
	before, err := r.pgContainer(volume)
	if err != nil {
		return err
	}
	if _, err := r.run("/usr/bin/docker", append(r.composeArgs(), "up", "-d", "--no-deps", "--no-recreate", "--wait", "--wait-timeout", "120", "postgres")...); err != nil {
		return err
	}
	afterVolume, err := r.pgVolume()
	a, _ := json.Marshal(volume)
	b, _ := json.Marshal(afterVolume)
	if err != nil || !bytes.Equal(a, b) {
		return fmt.Errorf("rotation PostgreSQL volume changed during recovery")
	}
	after, err := r.pgContainer(afterVolume)
	if err != nil {
		return err
	}
	if after == nil || !after.State.Running || after.State.Pid <= 0 || (before != nil && (before.ID != after.ID || before.Image != after.Image)) {
		return fmt.Errorf("rotation PostgreSQL recovery instance not stable/running")
	}
	return nil
}

func rotationLifecycle(compose string, args []string) error {
	if len(args) == 0 || len(args) > 6 {
		return fmt.Errorf("rotation lifecycle arguments refused")
	}
	bounded := args[0] == "activation-stop" || args[0] == "activation-running"
	if bounded && len(args) != 1 {
		return fmt.Errorf("rotation bounded stop/status takes no options")
	}
	allowed := map[string]bool{"up": true, "start": true, "stop": true, "restart": true, "down": true, "ps": true, "config": true, "activation-stop": true, "activation-running": true}
	if !allowed[args[0]] {
		return fmt.Errorf("rotation lifecycle action refused")
	}
	for _, arg := range args[1:] {
		if arg != "-d" && arg != "--quiet" && arg != "platform" && arg != "edge" && arg != "network" && arg != "collab" {
			return fmt.Errorf("rotation lifecycle unknown option/service refused")
		}
	}
	root := filepath.Join(compose, "var", "collab-operations")
	var input rotationInput
	// Binding only; no caller file is allowed to select a different project or recipe.
	raw, err := rotationRead(filepath.Join(root, "binding.json"))
	if err != nil || rotationJSON(raw, &input) != nil || input.SignedFile != "" || input.AnnouncementFile != "" || len(input.PrivateFiles) != 0 {
		return fmt.Errorf("rotation lifecycle binding refused")
	}
	r, err := newRotation(compose, input)
	if err != nil {
		return err
	}
	return r.lifecycle(args)
}

func (r *rotation) lifecycle(args []string) error {
	bounded := args[0] == "activation-stop" || args[0] == "activation-running"
	if bounded {
		command := []string{"stop", "edge", "network", "collab"}
		if args[0] == "activation-running" {
			command = []string{"ps", "--status", "running", "-q", "edge", "network", "collab"}
		}
		recipe := r.composeArgs()
		if recipe[len(recipe)-1] == filepath.Join(r.root(), "current.yml") {
			recipe = recipe[:len(recipe)-2]
		}
		output, err := r.run("/usr/bin/docker", append(recipe, command...)...)
		if err == nil {
			fmt.Print(string(output))
		}
		return err
	}
	if err := r.idle(); err != nil {
		return err
	}
	lockPath := filepath.Join(r.root(), ".lock")
	lock, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return fmt.Errorf("rotation lifecycle exclusive lock refused")
	}
	owner, _ := lock.Stat()
	defer func() {
		_ = lock.Close()
		if current, err := os.Lstat(lockPath); err == nil && os.SameFile(owner, current) {
			_ = os.Remove(lockPath)
		}
	}()
	receipts, err := r.archives()
	if err != nil || len(receipts) == 0 {
		return fmt.Errorf("rotation lifecycle history refused")
	}
	last := receipts[len(receipts)-1]
	if _, err := r.installed(last); err != nil {
		return err
	}
	starting := args[0] == "up" || args[0] == "start" || args[0] == "restart"
	if starting {
		if err := r.resumePostgres(); err != nil {
			return err
		}
	}
	if err := r.readStatus(r.expected(receipts)); err != nil {
		return err
	}
	if starting {
		work, err := os.MkdirTemp("", "auriga-key-start-check-")
		if err != nil {
			return err
		}
		defer func() { _ = os.RemoveAll(work) }()
		signed, ann := r.expected(receipts)
		if _, err := r.operator("verify", work, r.plan(signed, ann, signed, ann, r.state, nil)); err != nil {
			return err
		}
		raw, err := rotationRead(filepath.Join(r.versionDir(last.Version), "signed.json"))
		if err != nil {
			return err
		}
		_, keys, err := rotationPayload(raw)
		if err != nil {
			return err
		}
		if _, err := rotationActive(keys, time.Now()); err != nil {
			return err
		}
	}
	output, err := r.run("/usr/bin/docker", append(r.composeArgs(), args...)...)
	if err == nil {
		fmt.Print(string(output))
	}
	return err
}
