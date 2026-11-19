package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/hansjone/auriga/infra/agent/internal/renderer"
	"github.com/hansjone/auriga/infra/agent/internal/sharednode"
	"github.com/hansjone/auriga/infra/agent/internal/state"
	"github.com/hansjone/auriga/infra/agent/internal/storage"
	nodev1 "github.com/hansjone/auriga/infra/gen/auriga/node/v1"
	"google.golang.org/protobuf/proto"
)

type dedicatedPreparedFile struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	Digest string `json:"digest"`
}
type dedicatedPreparedManifest struct {
	SchemaVersion  int                     `json:"schemaVersion"`
	AttemptID      string                  `json:"attemptId"`
	Binding        json.RawMessage         `json:"binding"`
	View           json.RawMessage         `json:"view"`
	IntentDigest   string                  `json:"intentDigest"`
	BaselineDigest string                  `json:"baselineDigest"`
	TemplateDigest string                  `json:"templateDigest"`
	InputDigest    string                  `json:"inputDigest"`
	ImageID        string                  `json:"imageId"`
	ConfigDigest   string                  `json:"configDigest"`
	ConfigBytes    int64                   `json:"configBytes"`
	ProfileFiles   []dedicatedPreparedFile `json:"profileFiles"`
	StartedBundles []string                `json:"startedBundles"`
}

func dedicatedRead(root, file string, maximum int64) ([]byte, error) {
	if err := storage.CheckNoSymlink(root, file); err != nil {
		return nil, err
	}
	info, err := os.Lstat(file)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > maximum || info.Size() < 1 {
		return nil, fmt.Errorf("invalid dedicated preparation file")
	}
	f, err := os.Open(file)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	actual, err := f.Stat()
	if err != nil || !os.SameFile(info, actual) {
		return nil, fmt.Errorf("dedicated preparation file changed")
	}
	raw, err := io.ReadAll(io.LimitReader(f, maximum+1))
	if err != nil || len(raw) > int(maximum) {
		return nil, fmt.Errorf("dedicated preparation file budget")
	}
	return raw, nil
}

func strictDedicated(raw []byte, value any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	var trailing any
	if decoder.Decode(&trailing) != io.EOF {
		return fmt.Errorf("trailing dedicated preparation JSON")
	}
	return nil
}

// VerifyDedicatedPrepared checks the helper's CLOSED output before any mount is
// offered to a real DSH process. The helper's exit/provenance and current W view
// are independently fenced by the caller; this is not a readiness assertion.
func VerifyDedicatedPrepared(root, meta, output string, view *nodev1.SharedDshResourceView, intentDigest, attemptID, imageID string) (string, error) {
	if view == nil || !validPoolBinding(view.Binding) || view.Binding.Mode != nodev1.DshInstanceMode_DSH_INSTANCE_MODE_DEDICATED || !poolUUID.MatchString(attemptID) || !uDigest.MatchString(intentDigest) {
		return "", fmt.Errorf("dedicated preparation binding required")
	}
	if err := storage.CheckNoSymlink(root, output); err != nil {
		return "", err
	}
	entries, err := os.ReadDir(output)
	if err != nil || len(entries) != 2 {
		return "", fmt.Errorf("dedicated output must contain exactly config and manifest")
	}
	for _, entry := range entries {
		if !entry.Type().IsRegular() || (entry.Name() != "config.json" && entry.Name() != "manifest.json") {
			return "", fmt.Errorf("unexpected dedicated output")
		}
	}
	raw, err := dedicatedRead(root, filepath.Join(output, "manifest.json"), 4<<20)
	if err != nil {
		return "", err
	}
	object, err := DecodeUnifiedJSON(raw)
	if err != nil || len(object) != 13 {
		return "", fmt.Errorf("dedicated manifest requires the complete closed proof")
	}
	var proof dedicatedPreparedManifest
	if err = strictDedicated(raw, &proof); err != nil {
		return "", err
	}
	inputBytes, err := dedicatedRead(root, filepath.Join(meta, "dedicated-input.json"), 1<<20)
	if err != nil {
		return "", err
	}
	var input renderer.DedicatedPreparation
	if err = strictDedicated(inputBytes, &input); err != nil {
		return "", err
	}
	decodedView := new(nodev1.SharedDshResourceView)
	binding := new(nodev1.SharedDshBinding)
	if proof.SchemaVersion != 1 || input.SchemaVersion != 1 || proof.AttemptID != attemptID || proof.AttemptID != input.AttemptID ||
		proof.ImageID != imageID || proof.ImageID != input.ImageID || !uDigest.MatchString(imageID) ||
		proof.IntentDigest != intentDigest || proof.IntentDigest != input.IntentDigest ||
		proof.BaselineDigest != renderer.DedicatedBaselineDigest || proof.BaselineDigest != input.BaselineDigest ||
		proof.TemplateDigest != input.TemplateDigest || proof.InputDigest != udigest(inputBytes) ||
		sharednode.DecodeHostValue(proof.View, decodedView) != nil || !proto.Equal(decodedView, view) ||
		sharednode.DecodeHostValue(proof.Binding, binding) != nil || !proto.Equal(binding, view.Binding) ||
		!sameDedicatedJSON(proof.Binding, input.Binding) ||
		!sameDedicatedJSON(proof.View, input.View) || proof.ConfigBytes < 1 || proof.ConfigBytes > 4<<20 ||
		len(proof.ProfileFiles) < 3 || len(proof.ProfileFiles) > 16384 || len(proof.StartedBundles) > 256 {
		return "", fmt.Errorf("dedicated preparation proof mismatch")
	}
	config, err := dedicatedRead(root, filepath.Join(output, "config.json"), 4<<20)
	if err != nil || int64(len(config)) != proof.ConfigBytes || udigest(config) != proof.ConfigDigest {
		return "", fmt.Errorf("dedicated generated config mismatch")
	}
	var rows []map[string]json.RawMessage
	if err = json.Unmarshal(config, &rows); err != nil || len(rows) < 83 || len(rows) > 16384 {
		return "", fmt.Errorf("invalid dedicated generated entries")
	}
	logical, err := dedicatedRead(root, filepath.Join(meta, "dedicated-profile.json"), 1<<20)
	if err != nil || udigest(logical) != proof.TemplateDigest {
		return "", fmt.Errorf("dedicated template bytes mismatch")
	}
	var baseline []map[string]json.RawMessage
	if json.Unmarshal(logical, &baseline) != nil || len(baseline) != 83 {
		return "", fmt.Errorf("dedicated baseline missing")
	}
	required := map[string]map[string]json.RawMessage{}
	for _, row := range baseline {
		var id string
		if json.Unmarshal(row["id"], &id) != nil || id == "" {
			return "", fmt.Errorf("dedicated baseline identity missing")
		}
		required[id] = row
	}
	ids := map[string]bool{}
	for _, row := range rows {
		var id, name string
		if json.Unmarshal(row["id"], &id) != nil || id == "" || ids[id] || json.Unmarshal(row["name"], &name) != nil ||
			(!strings.HasPrefix(name, "file:///opt/dsh/") && name != renderer.DedicatedModule && !strings.HasPrefix(name, "file:///home/dsh/.host/.dsh/profiles/auriga-dedicated/node_modules/")) || strings.Contains(name, "/../") || strings.ContainsAny(name, "\\?#%") {
			return "", fmt.Errorf("dedicated generated module outside fixed roots")
		}
		ids[id] = true
		if original := required[id]; original != nil {
			original["name"] = row["name"]
			a, _ := json.Marshal(original)
			b, _ := json.Marshal(row)
			if !sameDedicatedJSON(a, b) {
				return "", fmt.Errorf("dedicated baseline was modified by preparation")
			}
			delete(required, id)
		} else if !strings.HasPrefix(name, "file:///home/dsh/.host/.dsh/profiles/auriga-dedicated/node_modules/") {
			return "", fmt.Errorf("extra dedicated entry must belong to an approved profile resource")
		}
		for key := range row {
			if key != "id" && key != "name" && key != "config" && key != "disabled" && key != "inject" {
				return "", fmt.Errorf("nonliteral dedicated generated row")
			}
		}
	}
	if len(required) != 0 {
		return "", fmt.Errorf("dedicated preparation omitted baseline providers")
	}
	seen := map[string]bool{}
	for _, file := range proof.ProfileFiles {
		if file.Size < 0 || file.Size > 268435456 || !uDigest.MatchString(file.Digest) || seen[file.Path] || strings.ContainsAny(file.Path, "\\\x00") ||
			(file.Path != "/home/dsh/.host/.dsh/cordis.patch.yml" && !strings.HasPrefix(file.Path, "/home/dsh/.host/.dsh/profiles/auriga-dedicated/")) || strings.Contains(file.Path, "/../") || strings.Contains(file.Path, "/./") {
			return "", fmt.Errorf("invalid dedicated profile file proof")
		}
		seen[file.Path] = true
	}
	for _, file := range []string{"package.json", "cordis.patch.yml", "pnpm-workspace.yaml"} {
		if !seen["/home/dsh/.host/.dsh/profiles/auriga-dedicated/"+file] {
			return "", fmt.Errorf("missing dedicated profile control proof")
		}
	}
	return proof.ConfigDigest, nil
}

func sameDedicatedJSON(a, b []byte) bool {
	left, err := DecodeUnifiedJSON(a)
	if err != nil {
		return false
	}
	right, err := DecodeUnifiedJSON(b)
	if err != nil {
		return false
	}
	x, err := unifiedCanonical(left)
	if err != nil {
		return false
	}
	y, err := unifiedCanonical(right)
	return err == nil && bytes.Equal(x, y)
}

type dedicatedHelperJournal struct {
	AttemptID, ContainerID, InputDigest, ImageID, Phase string
	Candidate                                           state.ReleaseCandidate
}

// prepareDedicated runs only inside the continuous signed release activation.
// Every mutation is consequently recorded by EngineClient's BeginAction seam.
func (d *Docker) prepareDedicated(ctx context.Context, spec *Spec, attempt, output, cache string, fence func() error) (result error) {
	scope, err := requireActivation(ctx, spec.InstanceID)
	if err != nil || scope.coordinator != d.Activation || spec.Pool == nil || spec.Full == nil || !proto.Equal(spec.Full, scope.spec) || spec.Image != scope.spec.GetDsh().GetImage() || spec.HostUID < 200000 || fence == nil {
		return fmt.Errorf("dedicated helper requires exact signed activation")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err = fence(); err != nil {
		return err
	}
	if stopped, err := d.mainProcessStopped(ctx, spec.InstanceID); err != nil || !stopped {
		return fmt.Errorf("dedicated old execution stop unconfirmed")
	}
	if err = d.Policy.AllowImage(spec.Image); err != nil {
		return err
	}
	if err = d.Client.PullImage(ctx, spec.Image); err != nil {
		return err
	}
	image, err := d.Client.ImageInspect(ctx, spec.Image)
	if err != nil {
		return err
	}
	imageID, _ := image["Id"].(string)
	if image["Os"] != scope.candidate.Partition.OS || image["Architecture"] != scope.candidate.Partition.Arch || !imageHasRepoDigest(image, spec.Image, scope.image.ManifestDigest()) {
		return fmt.Errorf("dedicated helper image mismatch")
	}
	if err = scope.pinEngineImage(imageID); err != nil {
		return err
	}
	home := map[string]any{"Type": "bind", "Source": filepath.Join(spec.Pool.Members, ".host"), "Target": "/home/dsh/.host", "ReadOnly": false}
	if spec.Pool.Volume != "" {
		if err = d.ensurePoolVolume(ctx, spec.InstanceID, spec.Pool, true); err != nil {
			return err
		}
		raw, _, err := d.Client.doJSON(ctx, http.MethodGet, "/version", nil, http.StatusOK)
		if err != nil {
			return err
		}
		var version struct {
			APIVersion string `json:"ApiVersion"`
		}
		if json.Unmarshal(raw, &version) != nil {
			return fmt.Errorf("dedicated helper Engine version unavailable")
		}
		major, minor, ok := strings.Cut(version.APIVersion, ".")
		apiMajor, e1 := strconv.Atoi(major)
		apiMinor, e2 := strconv.Atoi(minor)
		if !ok || e1 != nil || e2 != nil || apiMajor != 1 || apiMinor < 45 {
			return fmt.Errorf("dedicated quota-home volume subpath requires Engine API 1.45; refusing whole-pool writable helper")
		}
		home["Type"], home["Source"] = "volume", spec.Pool.Volume
		home["VolumeOptions"] = map[string]any{"NoCopy": true, "Subpath": ".host"}
	}
	cpu, mem, pids := d.Policy.ClampResources(spec.CPUMillis, spec.MemoryMB, spec.PIDs)
	environment := map[string]string{}
	imageConfig, _ := image["Config"].(map[string]any)
	if values, ok := imageConfig["Env"].([]any); ok {
		for _, value := range values {
			text, ok := value.(string)
			if !ok {
				return fmt.Errorf("invalid helper image environment")
			}
			key, val, ok := strings.Cut(text, "=")
			if !ok || validateEnvPair(key, val) != nil || strings.Contains(key, "TOKEN") || strings.Contains(key, "CREDENTIAL") || strings.Contains(key, "OWNER") {
				return fmt.Errorf("helper image contains personal environment")
			}
			if _, exists := environment[key]; exists {
				return fmt.Errorf("duplicate helper image environment")
			}
			environment[key] = val
		}
	}
	for key, value := range map[string]string{"HOME": "/home/dsh/.host", "DSH_HOME": "/home/dsh/.host/.dsh", "COREPACK_ENABLE_NETWORK": "0", "DSH_TELEMETRY_DISABLED": "1", "NODE_OPTIONS": "", "NODE_PATH": "", "LD_PRELOAD": ""} {
		environment[key] = value
	}
	var env []string
	for _, key := range sortedEnvKeys(environment) {
		env = append(env, key+"="+environment[key])
	}
	body := map[string]any{"Image": imageID, "User": fmt.Sprintf("%d:%d", spec.HostUID, spec.HostUID),
		"WorkingDir": "/home/dsh/.host", "Entrypoint": []string{"/usr/local/bin/node"}, "Cmd": []string{"/run/auriga/dedicated-prepare.mjs"},
		"Env":    env,
		"Labels": map[string]string{labelManaged: "true", labelInstance: spec.InstanceID, "auriga.dedicated-preparation": attempt},
		"HostConfig": map[string]any{"NetworkMode": "none", "ReadonlyRootfs": true, "Privileged": false, "CapDrop": []string{"ALL"}, "SecurityOpt": []string{"no-new-privileges:true"},
			"Memory": mem * 1024 * 1024, "NanoCpus": cpu * 1000000, "PidsLimit": pids, "RestartPolicy": map[string]any{"Name": "no"},
			"Tmpfs": map[string]string{"/tmp": "rw,noexec,nosuid,size=64m"}, "Mounts": []map[string]any{home,
				{"Type": "bind", "Source": spec.MetaBind, "Target": "/run/auriga", "ReadOnly": true},
				{"Type": "bind", "Source": cache, "Target": "/run/auriga/dedicated-resources", "ReadOnly": true},
				{"Type": "bind", "Source": output, "Target": "/run/auriga/dedicated-prepared", "ReadOnly": false}}}}
	journalFile := filepath.Join(filepath.Dir(spec.MetaBind), "helper-state")
	inputBytes, err := os.ReadFile(filepath.Join(spec.MetaBind, "dedicated-input.json"))
	if err != nil {
		return err
	}
	journal := dedicatedHelperJournal{AttemptID: attempt, ImageID: imageID, InputDigest: udigest(inputBytes), Phase: "creating", Candidate: scope.candidate}
	if err = unifiedAtomic(journalFile, journal); err != nil {
		return err
	}
	defer func() {
		if result != nil {
			journal.Phase = "closing"
			_ = unifiedAtomic(journalFile, journal)
		}
	}()
	name := "auriga-ded-prepare-" + attempt
	old, err := d.Client.ContainerInspect(ctx, name)
	if err != nil {
		return err
	}
	if old != nil {
		return fmt.Errorf("dedicated helper name already exists; exact cleanup required")
	}
	if err = fence(); err != nil {
		return err
	}
	if err = scope.recheck(); err != nil {
		return err
	}
	id, err := d.Client.ContainerCreate(ctx, name, body, scope.candidate.Partition.OS+"/"+scope.candidate.Partition.Arch)
	if err != nil {
		return err
	}
	journal.ContainerID, journal.Phase = id, "created"
	if err = unifiedAtomic(journalFile, journal); err != nil {
		return err
	}
	if err = d.verifyDedicatedHelper(ctx, id, body); err != nil {
		return err
	}
	created, err := d.Client.ContainerInspect(ctx, id)
	if err != nil {
		return err
	}
	createdState, _ := created["State"].(map[string]any)
	if createdState["Running"] != false || createdState["Status"] != "created" || createdState["Pid"] != float64(0) {
		return fmt.Errorf("dedicated helper already ran before the fixed Start")
	}
	if err = fence(); err != nil {
		return err
	}
	if err = scope.recheck(); err != nil {
		return err
	}
	journal.Phase = "starting"
	if err = unifiedAtomic(journalFile, journal); err != nil {
		return err
	}
	if err = d.Client.ContainerStart(ctx, id); err != nil {
		return err
	}
	raw, _, err := d.Client.doJSON(ctx, http.MethodPost, "/containers/"+url.PathEscape(id)+"/wait?condition=not-running", nil, http.StatusOK)
	if err != nil {
		return err
	}
	var waited struct {
		StatusCode int                       `json:"StatusCode"`
		Error      *struct{ Message string } `json:"Error"`
	}
	if json.Unmarshal(raw, &waited) != nil || waited.StatusCode != 0 || waited.Error != nil {
		return fmt.Errorf("dedicated preparation failed")
	}
	if err = d.verifyDedicatedHelper(ctx, id, body); err != nil {
		return err
	}
	actual, err := d.Client.ContainerInspect(ctx, id)
	if err != nil {
		return err
	}
	live, _ := actual["State"].(map[string]any)
	started, e1 := time.Parse(time.RFC3339Nano, stringValue(live["StartedAt"]))
	ended, e2 := time.Parse(time.RFC3339Nano, stringValue(live["FinishedAt"]))
	if live["Running"] != false || live["Status"] != "exited" || live["ExitCode"] != float64(0) || live["Pid"] != float64(0) || e1 != nil || e2 != nil || started.IsZero() || ended.Before(started) {
		return fmt.Errorf("dedicated helper termination unconfirmed")
	}
	if err = fence(); err != nil {
		return err
	}
	if err = scope.recheck(); err != nil {
		return err
	}
	if err = d.Client.ContainerRemove(ctx, id, false); err != nil {
		return err
	}
	if actual, err = d.Client.ContainerInspect(ctx, id); err != nil || actual != nil {
		return fmt.Errorf("dedicated helper removal unconfirmed")
	}
	journal.Phase = "exited"
	return unifiedAtomic(journalFile, journal)
}

func stringValue(value any) string { text, _ := value.(string); return text }

func (d *Docker) verifyDedicatedHelper(ctx context.Context, id string, body map[string]any) error {
	actual, err := d.Client.ContainerInspect(ctx, id)
	if err != nil {
		return err
	}
	if actual == nil || actual["Id"] != id || actual["Image"] != body["Image"] {
		return fmt.Errorf("dedicated helper identity mismatch")
	}
	cfg, _ := actual["Config"].(map[string]any)
	host, _ := actual["HostConfig"].(map[string]any)
	expected := body["HostConfig"].(map[string]any)
	if cfg["User"] != body["User"] || cfg["WorkingDir"] != body["WorkingDir"] || !sameStrings(cfg["Entrypoint"], []string{"/usr/local/bin/node"}) || !sameStrings(cfg["Cmd"], []string{"/run/auriga/dedicated-prepare.mjs"}) {
		return fmt.Errorf("dedicated helper command mismatch")
	}
	labels, _ := cfg["Labels"].(map[string]any)
	wantEnv := body["Env"].([]string)
	if !sameStrings(cfg["Env"], wantEnv) {
		return fmt.Errorf("dedicated helper environment mismatch")
	}
	for key, want := range body["Labels"].(map[string]string) {
		if labels[key] != want {
			return fmt.Errorf("dedicated helper ownership mismatch")
		}
	}
	for _, key := range []string{"NetworkMode", "ReadonlyRootfs", "Privileged", "Memory", "NanoCpus", "PidsLimit"} {
		a, _ := json.Marshal(host[key])
		b, _ := json.Marshal(expected[key])
		if !bytes.Equal(a, b) {
			return fmt.Errorf("dedicated helper containment mismatch")
		}
	}
	if !sameStrings(host["CapDrop"], []string{"ALL"}) || !sameStrings(host["SecurityOpt"], []string{"no-new-privileges:true"}) {
		return fmt.Errorf("dedicated helper capabilities mismatch")
	}
	for _, key := range []string{"CapAdd", "Binds", "Devices", "DeviceRequests", "VolumesFrom", "ExtraHosts", "GroupAdd", "PortBindings"} {
		value := host[key]
		if value != nil {
			raw, _ := json.Marshal(value)
			if string(raw) != "[]" && string(raw) != "{}" {
				return fmt.Errorf("dedicated helper has extra privilege")
			}
		}
	}
	for _, key := range []string{"PidMode", "IpcMode", "UsernsMode"} {
		if host[key] != nil && host[key] != "" && host[key] != "private" {
			return fmt.Errorf("dedicated helper namespace mismatch")
		}
	}
	restart, _ := host["RestartPolicy"].(map[string]any)
	if restart["Name"] != "no" {
		return fmt.Errorf("dedicated helper automatic restart denied")
	}
	mounts, _ := actual["Mounts"].([]any)
	want := expected["Mounts"].([]map[string]any)
	if len(mounts) != len(want) {
		return fmt.Errorf("dedicated helper mount count mismatch")
	}
	seenMounts := map[string]bool{}
	for _, row := range mounts {
		mount, _ := row.(map[string]any)
		destination, ok := mount["Destination"].(string)
		if !ok {
			return fmt.Errorf("invalid dedicated helper mount destination")
		}
		if seenMounts[destination] {
			return fmt.Errorf("duplicate dedicated helper mount")
		}
		seenMounts[destination] = true
		found := false
		for _, item := range want {
			if mount["Destination"] == item["Target"] {
				source := mount["Source"]
				if item["Type"] == "volume" {
					source = mount["Name"]
					configured, _ := host["Mounts"].([]any)
					subpath := false
					for _, row := range configured {
						actual, _ := row.(map[string]any)
						options, _ := actual["VolumeOptions"].(map[string]any)
						if actual["Target"] == item["Target"] && options["Subpath"] == ".host" && options["NoCopy"] == true {
							subpath = true
						}
					}
					if !subpath {
						return fmt.Errorf("dedicated quota home subpath proof missing")
					}
				}
				if mount["Type"] != item["Type"] || source != item["Source"] || mount["RW"] != (item["ReadOnly"] != true) {
					return fmt.Errorf("dedicated helper mount mismatch")
				}
				found = true
			}
		}
		if !found {
			return fmt.Errorf("dedicated helper unexpected mount")
		}
	}
	return nil
}
