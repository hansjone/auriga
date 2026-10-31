package main

import (
	"bytes"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const operatorMain = "auriga.collab.network.keys.NetworkKeyOperator"

// This is an installation-local format, not a network protocol or a database proof.
type rotationInput struct {
	Base             string            `json:"baseReceiptSha256"`
	Identity         string            `json:"baseIdentitySha256"`
	Jar              string            `json:"operatorJarSha256"`
	SignedFile       string            `json:"signedFile"`
	AnnouncementFile string            `json:"announcementFile"`
	PrivateFiles     map[string]string `json:"privateFiles"`
}

type rotationReceipt struct {
	Format   int               `json:"format"`
	Version  string            `json:"version"`
	Base     string            `json:"baseReceiptSha256"`
	Identity string            `json:"baseIdentitySha256"`
	Source   string            `json:"sourceReceiptSha256"`
	Jar      string            `json:"operatorJarSha256"`
	Previous string            `json:"previousVersion"`
	Files    map[string]string `json:"files"`
}

type operatorPlan struct {
	Config       string            `json:"configFile"`
	Root         string            `json:"rootFile"`
	Expected     string            `json:"expectedSignedFile"`
	ExpectedAnn  string            `json:"expectedAnnouncementFile"`
	Signed       string            `json:"signedFile"`
	Announcement string            `json:"announcementFile"`
	State        string            `json:"stateFile"`
	Private      map[string]string `json:"privateFiles"`
	History      string            `json:"expectedHistoryFile"`
	StateHash    string            `json:"expectedStateSha256"`
}

type rotation struct {
	compose, out, source, state, config, network, jar, project string
	input                                                      rotationInput
	sourceReceipt                                              string
	run                                                        func(string, ...string) ([]byte, error)
}

var rotationHash = regexp.MustCompile(`^[a-f0-9]{64}$`)

func rotationJSON(raw []byte, value any) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(value); err != nil || d.Decode(new(any)) != io.EOF {
		return fmt.Errorf("rotation JSON refused")
	}
	if err := rotationFieldNames(raw, reflect.TypeOf(value)); err != nil {
		return err
	}
	// encoding/json accepts duplicate names. Refuse them, including nested maps.
	var walk func() error
	d = json.NewDecoder(bytes.NewReader(raw))
	walk = func() error {
		t, err := d.Token()
		if err != nil {
			return err
		}
		if t == json.Delim('{') {
			seen := map[string]bool{}
			for d.More() {
				key, err := d.Token()
				if err != nil || seen[fmt.Sprint(key)] {
					return fmt.Errorf("duplicate rotation field")
				}
				seen[fmt.Sprint(key)] = true
				if err := walk(); err != nil {
					return err
				}
			}
			_, err = d.Token()
		} else if t == json.Delim('[') {
			for d.More() {
				if err := walk(); err != nil {
					return err
				}
			}
			_, err = d.Token()
		}
		return err
	}
	return walk()
}

func rotationFieldNames(raw []byte, kind reflect.Type) error {
	if kind.Kind() == reflect.Pointer {
		return rotationFieldNames(raw, kind.Elem())
	}
	if kind.Kind() != reflect.Struct {
		return nil
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return fmt.Errorf("rotation object refused")
	}
	allowed := map[string]reflect.Type{}
	for i := 0; i < kind.NumField(); i++ {
		field := kind.Field(i)
		name := strings.Split(field.Tag.Get("json"), ",")[0]
		if name == "" {
			name = field.Name
		}
		allowed[name] = field.Type
	}
	for name, value := range fields {
		field, exists := allowed[name]
		if !exists {
			return fmt.Errorf("rotation field spelling refused")
		}
		if err := rotationFieldNames(value, field); err != nil {
			return err
		}
	}
	return nil
}

func rotationRead(path string) ([]byte, error) {
	if err := rotationInputParents(path); err != nil {
		return nil, err
	}
	return collabRead(path, 1<<20, true)
}

func rotationWrite(path string, raw []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return fmt.Errorf("rotation new file refused")
	}
	_, err = f.Write(raw)
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	return closeErr
}

func rotationWriteJSON(path string, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return rotationWrite(path, raw)
}

func rotationEnvironment() error {
	for _, entry := range os.Environ() {
		key, value, _ := strings.Cut(entry, "=")
		if value != "" && (strings.HasPrefix(key, "COMPOSE_") || strings.HasPrefix(key, "DOCKER_") || strings.HasPrefix(key, "LOADER_") || key == "JAVA_TOOL_OPTIONS" || key == "JDK_JAVA_OPTIONS" || key == "_JAVA_OPTIONS" || key == "CLASSPATH") {
			return fmt.Errorf("rotation refuses process/recipe override environment")
		}
	}
	return nil
}

func newRotation(compose string, input rotationInput) (*rotation, error) {
	if !rotationHash.MatchString(input.Base) || !rotationHash.MatchString(input.Identity) || !rotationHash.MatchString(input.Jar) {
		return nil, fmt.Errorf("rotation checkpoint missing")
	}
	compose, err := filepath.Abs(compose)
	if err != nil {
		return nil, err
	}
	r := &rotation{compose: compose, out: filepath.Join(compose, "var"), input: input}
	r.jar = filepath.Join(filepath.Dir(filepath.Dir(compose)), "services", "platform-app", "target", "platform-app-0.1.0-SNAPSHOT-exec.jar")
	r.run = func(program string, args ...string) ([]byte, error) {
		if program != "/usr/bin/java" && program != "/usr/bin/docker" {
			return nil, fmt.Errorf("rotation executable refused")
		}
		if program == "/usr/bin/docker" {
			config := filepath.Join(r.root(), "docker-client")
			if _, err := os.Lstat(config); err == nil {
				entries, err := os.ReadDir(config)
				if !rotationDirectory(config) || err != nil || len(entries) != 0 {
					return nil, fmt.Errorf("rotation Docker client config must be empty and protected")
				}
			} else if !os.IsNotExist(err) {
				return nil, fmt.Errorf("rotation Docker client config refused")
			}
			args = append([]string{"--host=unix:///var/run/docker.sock", "--config=" + config}, args...)
		}
		command := exec.Command(program, args...)
		command.Dir = compose
		command.Env = []string{"PATH=/usr/bin:/bin", "LANG=C.UTF-8"}
		b, err := command.CombinedOutput()
		if err != nil {
			// Child diagnostics may contain JDBC configuration. Preserve a bounded stage failure only.
			if failure, ok := err.(*exec.ExitError); ok {
				return nil, fmt.Errorf("fixed rotation subprocess rejected operation (exit %d)", failure.ExitCode())
			}
			return nil, fmt.Errorf("fixed rotation subprocess failed to start")
		}
		return b, nil
	}
	if err := r.binding(); err != nil {
		return nil, err
	}
	return r, nil
}

func (r *rotation) binding() error {
	receipt, err := rotationRead(filepath.Join(r.out, "iam-installation", "bootstrap.receipt"))
	if err != nil || digestBytes(receipt) != r.input.Base {
		return fmt.Errorf("rotation base checkpoint refused")
	}
	identity, err := collabRead(filepath.Join(r.out, "identity-keys.json"), 65536, false)
	if err != nil || digestBytes(identity) != r.input.Identity {
		return fmt.Errorf("rotation identity checkpoint refused")
	}
	r.source, r.state, r.network = r.out, filepath.Join(r.out, "collab-runtime", "state", "state.json"), filepath.Join(r.out, "network")
	r.config = filepath.Join(r.out, "collab-runtime", "config")
	if _, err := os.Lstat(filepath.Join(r.out, activationDirectory)); err == nil {
		a, err := checkActivation(r.out)
		if err != nil || a.Base != r.input.Base || a.Identity != r.input.Identity {
			return fmt.Errorf("rotation activation binding refused")
		}
		r.source = filepath.Join(r.out, activationDirectory)
		r.state = filepath.Join(r.out, "collab-activation-runtime", "collab-runtime", "state", "state.json")
		r.config = filepath.Join(r.out, "collab-activation-runtime", "collab-runtime", "config")
		r.network = filepath.Join(r.out, "collab-activation-runtime", "network")
		receipt, err = rotationRead(filepath.Join(r.source, "activation.receipt"))
		if err != nil {
			return err
		}
	} else {
		lines := strings.Split(string(receipt), "\n")
		if len(lines) != 3 {
			return fmt.Errorf("rotation base receipt invalid")
		}
		if _, _, reuse, err := existingIdentityMaterials(r.out, lines[0]); err != nil || !reuse || !validateCollabSource(r.out) {
			return fmt.Errorf("rotation initial N1 seal refused")
		}
	}
	r.sourceReceipt = digestBytes(receipt)
	keys, err := rotationRead(filepath.Join(r.out, "iam-installation", "compose.keys"))
	if err != nil {
		return err
	}
	defer clear(keys)
	access, err := collabRead(filepath.Join(r.out, "access.env"), 65536, false)
	if err != nil {
		return err
	}
	ca, err := collabRead(filepath.Join(r.out, "ca-sha256.txt"), 65536, false)
	if err != nil {
		return err
	}
	env, err := rotationRead(filepath.Join(r.out, "compose.env"))
	defer clear(env)
	if err != nil || !matchesPanelComposeEnvironment(r.out, keys, ca, access, env) {
		return fmt.Errorf("rotation compose environment differs from sealed inputs")
	}
	match := regexp.MustCompile(`(?m)^COMPOSE_PROJECT_NAME='([a-z0-9][a-z0-9_-]*)'$`).FindSubmatch(access)
	if len(match) != 2 {
		return fmt.Errorf("rotation fixed project missing")
	}
	r.project = string(match[1])
	if err := rotationInputParents(r.jar); err != nil {
		return err
	}
	jar, err := collabRead(r.jar, 1<<30, false)
	if err != nil || digestBytes(jar) != r.input.Jar {
		return fmt.Errorf("rotation fixed operator jar identity refused")
	}
	return nil
}

func (r *rotation) root() string { return filepath.Join(r.out, "collab-operations") }
func (r *rotation) versionDir(version string) string {
	return filepath.Join(r.root(), "releases", version)
}

func (r *rotation) plan(signed, ann, previous, previousAnn, state string, secrets map[string]string) operatorPlan {
	if secrets == nil {
		secrets = map[string]string{}
	}
	history := ""
	if previous != "" {
		candidate := filepath.Join(filepath.Dir(previous), "pg-status.json")
		if _, err := os.Lstat(candidate); err == nil {
			history = candidate
		}
	}
	stateHash := ""
	if raw, err := rotationRead(filepath.Join(r.source, "collab-installation", "state.json")); err == nil {
		stateHash = digestBytes(raw)
	}
	if raw, err := rotationRead(filepath.Join(r.root(), "current.json")); err == nil {
		stateHash = ""
		var current rotationCurrent
		if rotationJSON(raw, &current) == nil && rotationVersion(current.Version) && rotationHash.MatchString(current.Receipt) {
			raw, err := rotationRead(filepath.Join(r.root(), "installs", current.Version, "install.receipt"))
			var installed rotationInstallReceipt
			if err == nil && digestBytes(raw) == current.Receipt && rotationJSON(raw, &installed) == nil && installed.Binding.Base == r.input.Base && installed.Binding.Identity == r.input.Identity && installed.Binding.Source == r.sourceReceipt && installed.Binding.Jar == r.input.Jar && installed.Binding.Version == current.Version && rotationHash.MatchString(installed.State) {
				stateHash = installed.State
			}
		}
	}
	return operatorPlan{filepath.Join(r.source, "collab-installation", "collab.properties"), filepath.Join(r.source, "collab-installation", "root.der"), previous, previousAnn, signed, ann, state, secrets, history, stateHash}
}

func rotationVersion(value string) bool {
	n, err := strconv.ParseInt(value, 10, 64)
	return err == nil && n > 0 && strconv.FormatInt(n, 10) == value
}

func (r *rotation) operator(action, work string, plan operatorPlan) ([]byte, error) {
	path := filepath.Join(work, action+"-plan.json")
	if err := rotationWriteJSON(path, plan); err != nil {
		return nil, err
	}
	loader := filepath.Join(work, action+"-loader.properties")
	if err := rotationWrite(loader, []byte{}); err != nil {
		return nil, err
	}
	location := (&url.URL{Scheme: "file", Path: filepath.ToSlash(loader)}).String()
	output, err := r.run("/usr/bin/java", "-Dloader.main="+operatorMain, "-Dloader.config.location="+location, "-cp", r.jar, "org.springframework.boot.loader.launch.PropertiesLauncher", action, path)
	if err != nil {
		return nil, fmt.Errorf("rotation operator %s: %w", action, err)
	}
	return output, nil
}

func (r *rotation) archives() ([]rotationReceipt, error) {
	entries, err := os.ReadDir(filepath.Join(r.root(), "releases"))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !rotationDirectory(r.root()) || !rotationDirectory(filepath.Join(r.root(), "releases")) {
		return nil, fmt.Errorf("rotation archive directory DAC refused")
	}
	var receipts []rotationReceipt
	for _, entry := range entries {
		version, err := strconv.ParseInt(entry.Name(), 10, 64)
		if err != nil || version < 1 || strconv.FormatInt(version, 10) != entry.Name() || !entry.IsDir() {
			return nil, fmt.Errorf("rotation archive path refused")
		}
		var receipt rotationReceipt
		dir := r.versionDir(entry.Name())
		raw, err := rotationRead(filepath.Join(dir, "publication.receipt"))
		if err != nil || rotationJSON(raw, &receipt) != nil || receipt.Format != 1 || receipt.Version != entry.Name() || receipt.Base != r.input.Base || receipt.Identity != r.input.Identity || receipt.Source != r.sourceReceipt || receipt.Jar != r.input.Jar {
			return nil, fmt.Errorf("rotation archive binding refused")
		}
		if len(receipt.Files) != 3 || receipt.Files["signed.json"] == "" || receipt.Files["announcement.json"] == "" || receipt.Files["pg-status.json"] == "" {
			return nil, fmt.Errorf("rotation incomplete publication seal")
		}
		if err := rotationCheckFiles(dir, receipt.Files); err != nil {
			return nil, err
		}
		receipts = append(receipts, receipt)
	}
	sort.Slice(receipts, func(i, j int) bool {
		a, _ := strconv.ParseInt(receipts[i].Version, 10, 64)
		b, _ := strconv.ParseInt(receipts[j].Version, 10, 64)
		return a < b
	})
	if len(receipts) > 0 {
		raw, err := rotationRead(filepath.Join(r.source, "collab-installation", "signed.json"))
		if err != nil {
			return nil, err
		}
		initial, _, err := rotationPayload(raw)
		if err != nil || receipts[0].Previous != initial {
			return nil, fmt.Errorf("rotation first archive/history missing")
		}
	}
	for i := 1; i < len(receipts); i++ {
		if receipts[i].Previous != receipts[i-1].Version {
			return nil, fmt.Errorf("rotation release history missing")
		}
	}
	return receipts, nil
}

func rotationCheckFiles(dir string, expected map[string]string) error {
	if len(expected) == 0 {
		return fmt.Errorf("rotation seal empty")
	}
	for name, hash := range expected {
		if !fs.ValidPath(name) || !rotationHash.MatchString(hash) {
			return fmt.Errorf("rotation seal path refused")
		}
		raw, err := rotationRead(filepath.Join(dir, filepath.FromSlash(name)))
		if err != nil || digestBytes(raw) != hash {
			clear(raw)
			return fmt.Errorf("rotation sealed bytes changed")
		}
		clear(raw)
	}
	if err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, path)
		if rel == "." {
			return nil
		}
		if entry.IsDir() || (rel != "publication.receipt" && expected[filepath.ToSlash(rel)] == "") {
			return fmt.Errorf("rotation unknown archive material")
		}
		return nil
	}); err != nil {
		return err
	}
	return nil
}

func (r *rotation) expected(receipts []rotationReceipt) (string, string) {
	dir := filepath.Join(r.source, "collab-installation")
	if len(receipts) > 0 {
		dir = r.versionDir(receipts[len(receipts)-1].Version)
	}
	return filepath.Join(dir, "signed.json"), filepath.Join(dir, "announcement.json")
}

func (r *rotation) idle() error {
	for _, name := range []string{".lock", ".pending"} {
		if _, err := os.Lstat(filepath.Join(r.root(), name)); !os.IsNotExist(err) {
			return fmt.Errorf("rotation interrupted or locked; retain first evidence; no automatic repair")
		}
	}
	return nil
}

func (r *rotation) mutate(action string, body func(string) error) (result error) {
	for _, dir := range []string{r.root(), filepath.Join(r.root(), "releases"), filepath.Join(r.root(), "installs")} {
		if _, err := os.Lstat(dir); os.IsNotExist(err) {
			if err := os.Mkdir(dir, 0700); err != nil {
				return err
			}
		} else if err != nil || !rotationDirectory(dir) {
			return fmt.Errorf("rotation directory metadata refused")
		}
	}
	if err := r.idle(); err != nil {
		return err
	}
	lockPath := filepath.Join(r.root(), ".lock")
	lock, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return fmt.Errorf("rotation exclusive lock refused")
	}
	owner, _ := lock.Stat()
	defer func() {
		_ = lock.Close()
		if info, err := os.Lstat(lockPath); err == nil && os.SameFile(owner, info) {
			_ = os.Remove(lockPath)
		}
	}()
	work := filepath.Join(r.root(), ".pending")
	if err := os.Mkdir(work, 0700); err != nil {
		return err
	}
	workInfo, err := os.Lstat(work)
	if err != nil {
		return err
	}
	defer func() {
		current, err := os.Lstat(work)
		if result != nil && err == nil && os.SameFile(workInfo, current) {
			_ = rotationWriteJSON(filepath.Join(work, "first-failure.json"), map[string]string{"action": action, "status": "incomplete", "reason": result.Error()})
		}
	}()
	binding := rotationInput{Base: r.input.Base, Identity: r.input.Identity, Jar: r.input.Jar}
	if raw, err := rotationRead(filepath.Join(r.root(), "binding.json")); err == nil {
		var old rotationInput
		if rotationJSON(raw, &old) != nil || old.Base != binding.Base || old.Identity != binding.Identity || old.Jar != binding.Jar || old.SignedFile != "" || old.AnnouncementFile != "" || len(old.PrivateFiles) != 0 {
			return fmt.Errorf("rotation operations binding changed")
		}
	} else if err := rotationWriteJSON(filepath.Join(r.root(), "binding.json"), binding); err != nil {
		return err
	}
	if err := body(work); err != nil {
		return err
	}
	// Only remove files created by this successful operation. Failed stages are never repaired.
	current, err := os.Lstat(work)
	if err != nil || !os.SameFile(workInfo, current) {
		return fmt.Errorf("rotation pending instance changed; no cleanup")
	}
	return os.RemoveAll(work)
}

func rotationPayload(raw []byte) (string, []map[string]string, error) {
	var signed struct {
		Keyset struct {
			Version string              `json:"keyset_version"`
			Keys    []map[string]string `json:"keys"`
		} `json:"keyset"`
	}
	if json.Unmarshal(raw, &signed) != nil {
		return "", nil, fmt.Errorf("rotation manifest unreadable")
	}
	v, err := strconv.ParseInt(signed.Keyset.Version, 10, 64)
	if err != nil || v < 1 || strconv.FormatInt(v, 10) != signed.Keyset.Version {
		return "", nil, fmt.Errorf("rotation version refused")
	}
	return signed.Keyset.Version, signed.Keyset.Keys, nil
}

func (r *rotation) publish() error {
	if err := r.idle(); err != nil {
		return err
	}
	if err := r.liveCheckpoint(); err != nil {
		return err
	}
	signed, err := rotationRead(r.input.SignedFile)
	if err != nil {
		return err
	}
	ann, err := rotationRead(r.input.AnnouncementFile)
	if err != nil {
		return err
	}
	version, _, err := rotationPayload(signed)
	if err != nil {
		return err
	}
	if len(r.input.PrivateFiles) != 0 {
		return fmt.Errorf("public publication input must not carry private files")
	}
	receipts, err := r.archives()
	if err != nil {
		return err
	}
	previous, previousAnn := r.expected(receipts)
	if len(receipts) > 0 && receipts[len(receipts)-1].Version == version {
		last := receipts[len(receipts)-1]
		if last.Files["signed.json"] != digestBytes(signed) || last.Files["announcement.json"] != digestBytes(ann) {
			return fmt.Errorf("rotation same version changed bytes")
		}
		return r.readStatus(previous, previousAnn)
	}
	previousRaw, err := rotationRead(previous)
	if err != nil {
		return err
	}
	previousVersion, _, err := rotationPayload(previousRaw)
	if err != nil {
		return err
	}
	previousNumber, _ := strconv.ParseInt(previousVersion, 10, 64)
	nextNumber, _ := strconv.ParseInt(version, 10, 64)
	if nextNumber <= previousNumber {
		return fmt.Errorf("rotation requires a higher new publication version")
	}
	return r.mutate("publish", func(work string) error {
		stage := filepath.Join(work, "release")
		if err := os.Mkdir(stage, 0700); err != nil {
			return err
		}
		for name, raw := range map[string][]byte{"signed.json": signed, "announcement.json": ann} {
			if err := rotationWrite(filepath.Join(stage, name), raw); err != nil {
				return err
			}
		}
		plan := r.plan(filepath.Join(stage, "signed.json"), filepath.Join(stage, "announcement.json"), previous, previousAnn, r.state, nil)
		if _, err := r.operator("verify", work, plan); err != nil {
			return err
		}
		if _, err := r.operator("preflight", work, plan); err != nil {
			return err
		}
		if err := r.stop(work); err != nil {
			return err
		}
		status, err := r.operator("publish", work, plan)
		if err != nil {
			return err
		}
		var proof struct {
			Version        string            `json:"version"`
			DatabaseTime   string            `json:"databaseTime"`
			Pending        bool              `json:"pendingPush"`
			ManifestDigest string            `json:"manifestDigest"`
			First          map[string]string `json:"firstPublished"`
		}
		if rotationJSON(bytes.TrimSpace(status), &proof) != nil || proof.Version != version || len(proof.First) == 0 {
			return fmt.Errorf("rotation publication acknowledgement refused")
		}
		if err := rotationWrite(filepath.Join(stage, "pg-status.json"), status); err != nil {
			return err
		}
		previousRaw, err := rotationRead(previous)
		if err != nil {
			return err
		}
		previousVersion, _, err := rotationPayload(previousRaw)
		if err != nil {
			return err
		}
		receipt := rotationReceipt{1, version, r.input.Base, r.input.Identity, r.sourceReceipt, r.input.Jar, previousVersion, map[string]string{"signed.json": digestBytes(signed), "announcement.json": digestBytes(ann), "pg-status.json": digestBytes(status)}}
		if err := rotationWriteJSON(filepath.Join(stage, "publication.receipt"), receipt); err != nil {
			return err
		}
		if err := r.binding(); err != nil {
			return err
		}
		return os.Rename(stage, r.versionDir(version))
	})
}

func (r *rotation) readStatus(signed, ann string) error {
	// Read-only temporary command plans never become deployment receipts.
	work, err := os.MkdirTemp("", "auriga-key-status-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(work) }()
	b, err := r.operator("status", work, r.plan(signed, ann, signed, ann, r.state, nil))
	if err == nil {
		fmt.Print(string(b))
	}
	return err
}

func keyRotationCLI(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("expected key-rotation publish|install|status input.json, or lifecycle action")
	}
	if err := rotationEnvironment(); err != nil {
		return err
	}
	if !rotationSupported() {
		return fmt.Errorf("key rotation requires Linux installer root; no deployment accessed")
	}
	compose, err := filepath.Abs("..")
	if err != nil {
		return err
	}
	if args[0] == "lifecycle" {
		return rotationLifecycle(compose, args[1:])
	}
	if len(args) != 2 || (args[0] != "publish" && args[0] != "install" && args[0] != "status") {
		return fmt.Errorf("unknown rotation action or arguments")
	}
	path, err := filepath.Abs(args[1])
	if err != nil {
		return err
	}
	raw, err := rotationRead(path)
	var input rotationInput
	if err != nil || rotationJSON(raw, &input) != nil {
		return fmt.Errorf("rotation input refused")
	}
	resolve := func(value string) string {
		if value != "" && !filepath.IsAbs(value) {
			return filepath.Join(filepath.Dir(path), value)
		}
		return value
	}
	input.SignedFile, input.AnnouncementFile = resolve(input.SignedFile), resolve(input.AnnouncementFile)
	for kid, value := range input.PrivateFiles {
		input.PrivateFiles[kid] = resolve(value)
	}
	r, err := newRotation(compose, input)
	if err != nil {
		return err
	}
	switch args[0] {
	case "publish":
		return r.publish()
	case "install":
		return r.install()
	default:
		if err := r.idle(); err != nil {
			return err
		}
		if input.SignedFile != "" || input.AnnouncementFile != "" || len(input.PrivateFiles) != 0 {
			return fmt.Errorf("status takes checkpoints only")
		}
		receipts, err := r.archives()
		if err != nil {
			return err
		}
		signed, ann := r.expected(receipts)
		return r.readStatus(signed, ann)
	}
}

// No crypto/state decisions here: selecting gateway wiring only follows a Java-verified payload.
func rotationActive(keys []map[string]string, now time.Time) (map[string]string, error) {
	active := map[string]string{}
	for _, key := range keys {
		if key["state"] != "active" {
			continue
		}
		before, eb := time.Parse(time.RFC3339, key["not_before"])
		after, ea := time.Parse(time.RFC3339, key["not_after"])
		if eb != nil || ea != nil || now.Before(before) || !now.Before(after) || active[key["purpose"]] != "" {
			return nil, fmt.Errorf("rotation active signing key unavailable")
		}
		active[key["purpose"]] = key["kid"]
	}
	if len(active) != 3 || active["business_delivery"] == "" || active["protocol_response"] == "" || active["management_state"] == "" {
		return nil, fmt.Errorf("rotation requires three valid active keys before restarting; keep recovery publication stopped")
	}
	return active, nil
}

func rotationPEM(der []byte) []byte {
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
}
