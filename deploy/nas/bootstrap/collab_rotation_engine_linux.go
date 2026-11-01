package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

const nasRotationProject = "auriga-nas-test"

type nasRotationEngine struct {
	client *http.Client
	self   string
}
type nasRotationContainer struct {
	ID     string `json:"Id"`
	Image  string
	Config struct {
		Image      string
		Labels     map[string]string
		Entrypoint []string
	}
	State struct {
		Running, Paused, Restarting bool
		Pid                         int
		Health                      struct{ Status string }
	}
	Mounts []struct {
		Source, Destination string
		RW                  bool
	}
}

func (e *nasRotationEngine) call(method, path string, body any) ([]byte, error) {
	var input io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		input = bytes.NewReader(b)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, "http://docker/v1.41"+path, input)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := e.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("NAS rotation Engine unavailable")
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20+1))
	if err != nil || len(b) > 8<<20 || resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("NAS rotation Engine rejected %s (status %d)", method, resp.StatusCode)
	}
	return b, nil
}
func (e *nasRotationEngine) inspect(id string) (nasRotationContainer, error) {
	var c nasRotationContainer
	b, err := e.call("GET", "/containers/"+url.PathEscape(id)+"/json", nil)
	if err != nil {
		return c, err
	}
	err = json.Unmarshal(b, &c)
	if err != nil || !nasRotationDigest.MatchString(c.ID) {
		return c, fmt.Errorf("NAS rotation container response refused")
	}
	return c, nil
}
func (e *nasRotationEngine) containers() ([]nasRotationContainer, error) {
	b, err := e.call("GET", "/containers/json?all=true", nil)
	if err != nil {
		return nil, err
	}
	var ids []struct {
		ID string `json:"Id"`
	}
	if json.Unmarshal(b, &ids) != nil || len(ids) > 4096 {
		return nil, fmt.Errorf("NAS rotation inventory refused")
	}
	var result []nasRotationContainer
	for _, item := range ids {
		if !nasRotationDigest.MatchString(item.ID) {
			return nil, fmt.Errorf("NAS rotation inventory id refused")
		}
		c, err := e.inspect(item.ID)
		if err != nil {
			return nil, err
		}
		result = append(result, c)
	}
	return result, nil
}
func (e *nasRotationEngine) owned(r *nasRotation, c nasRotationContainer) error {
	labels := c.Config.Labels
	if labels["com.docker.compose.project"] != nasRotationProject || labels["com.docker.compose.project.working_dir"] != r.recipe {
		return fmt.Errorf("NAS rotation foreign project or recipe refused")
	}
	files := strings.Split(labels["com.docker.compose.project.config_files"], ",")
	main := false
	for _, f := range files {
		if f == filepath.Join(r.recipe, "docker-compose.yml") {
			main = true
			continue
		}
		if f == filepath.Join(r.recipe, "collab-compose.yml") {
			continue
		}
		parent := filepath.Dir(r.data)
		if (strings.HasPrefix(f, parent+"/.auriga-compose.") || strings.HasPrefix(f, parent+"/.auriga-runtime-compose.")) && (filepath.Base(f) == "override.yml" || filepath.Base(f) == "runtime.yml" || filepath.Base(f) == "rotation.yml") {
			continue
		}
		return fmt.Errorf("NAS rotation foreign Compose file refused")
	}
	if !main {
		return fmt.Errorf("NAS rotation missing fixed recipe")
	}
	if c.Config.Labels["com.docker.compose.service"] == "collab" || c.Config.Labels["com.docker.compose.service"] == "platform" {
		expected := r.controlImage
		if expected == "" {
			expected = r.binding.Image
		}
		if c.Image != expected {
			return fmt.Errorf("NAS rotation platform image differs from approval")
		}
	}
	return nil
}
func (e *nasRotationEngine) stop(r *nasRotation) error {
	all, err := e.containers()
	if err != nil {
		return err
	}
	services := map[string]nasRotationContainer{}
	for _, c := range all {
		if c.Config.Labels["com.docker.compose.project"] != nasRotationProject {
			continue
		}
		s := c.Config.Labels["com.docker.compose.service"]
		if s != "edge" && s != "network" && s != "collab" && s != "platform" {
			continue
		}
		if _, ok := services[s]; ok {
			return fmt.Errorf("NAS rotation duplicate service refused")
		}
		if err = e.owned(r, c); err != nil {
			return err
		}
		expectedMounts := map[string]string{}
		config := filepath.Join(r.data, "config/collab-runtime/collab")
		protocol := filepath.Join(r.data, "config/collab-runtime/network/protocol.key")
		if _, err := os.Lstat(filepath.Join(r.root, "current.json")); err == nil && !r.stopOnly {
			rows, err := r.releases()
			if err != nil {
				return err
			}
			dir, _, err := r.installed(rows, false)
			if err != nil {
				return err
			}
			config = filepath.Join(dir, "config")
			protocol = filepath.Join(dir, "network/protocol.key")
		}
		switch s {
		case "collab":
			expectedMounts["/etc/auriga/collab"] = config
		case "network":
			expectedMounts["/etc/auriga/network/protocol.key"] = protocol
			expectedMounts["/etc/auriga/network/runtime.json"] = filepath.Join(r.data, "config/network-policy/runtime.json")
		case "platform":
			expectedMounts["/etc/auriga/network"] = filepath.Join(r.data, "config/collab-runtime/platform")
		case "edge":
			expectedMounts["/etc/auriga/edge.config.json"] = filepath.Join(r.data, "config/collab-runtime/edge/edge.config.json")
		}
		for target, source := range expectedMounts {
			found := false
			for _, m := range c.Mounts {
				if m.Destination == target {
					found = m.Source == source && !m.RW
					if r.stopOnly && !m.RW && (target == "/etc/auriga/collab" || target == "/etc/auriga/network/protocol.key") {
						rel, e := filepath.Rel(filepath.Join(r.root, "installs"), m.Source)
						parts := strings.Split(filepath.ToSlash(rel), "/")
						if e == nil && len(parts) >= 2 && nasRotationVersion(parts[0]) && ((target == "/etc/auriga/collab" && len(parts) == 2 && parts[1] == "config") || (target == "/etc/auriga/network/protocol.key" && len(parts) == 3 && parts[1] == "network" && parts[2] == "protocol.key")) {
							found = true
						}
					}
				}
			}
			if !found {
				return fmt.Errorf("NAS rotation control mount differs from sealed installation")
			}
		}
		if s == "collab" {
			found := false
			for _, m := range c.Mounts {
				if m.Destination == "/var/lib/auriga/collab" {
					found = m.Source == filepath.Dir(r.state) && m.RW
				}
			}
			if !found {
				return fmt.Errorf("NAS rotation original signer state mount required")
			}
		}
		services[s] = c
	}
	if len(services) != 4 {
		return fmt.Errorf("NAS rotation requires four existing control services")
	}
	for _, s := range []string{"network", "collab"} {
		c := services[s]
		if c.State.Paused || c.State.Restarting {
			return fmt.Errorf("NAS rotation unstable control process")
		}
		if c.State.Running {
			if _, err = e.call("POST", "/containers/"+c.ID+"/stop?t=30", nil); err != nil {
				return err
			}
		}
		after, err := e.inspect(c.ID)
		if err != nil || after.Image != c.Image || after.State.Running || after.State.Paused || after.State.Restarting || after.State.Pid != 0 {
			return fmt.Errorf("NAS rotation signer did not stop")
		}
	}
	all, err = e.containers()
	if err != nil {
		return err
	}
	for _, c := range all {
		if c.ID == e.self {
			continue
		}
		if !c.State.Running && !c.State.Paused && !c.State.Restarting && c.State.Pid == 0 {
			continue
		}
		for _, m := range c.Mounts {
			if c.Config.Labels["com.docker.compose.service"] == "collab-renew" && c.Config.Labels["com.docker.compose.project"] == nasRotationProject && (m.Source == r.data && !m.RW || m.Source == filepath.Dir(r.state) && m.RW) && len(c.Config.Entrypoint) == 2 && c.Config.Entrypoint[0] == "/bin/sh" && c.Config.Entrypoint[1] == filepath.Join(r.recipe, "scripts/renew-collab-loop.sh") {
				if err := e.owned(r, c); err != nil {
					return err
				}
				continue
			}
			rel, err := filepath.Rel(m.Source, r.state)
			if err == nil && rel != ".." && !strings.HasPrefix(rel, "../") {
				return fmt.Errorf("NAS rotation another live process can access original signer state")
			}
		}
	}
	return nil
}
func (e *nasRotationEngine) database(r *nasRotation) (string, error) {
	all, err := e.containers()
	if err != nil {
		return "", err
	}
	var pg *nasRotationContainer
	for i := range all {
		c := &all[i]
		if c.Config.Labels["com.docker.compose.project"] == nasRotationProject && c.Config.Labels["com.docker.compose.service"] == "postgres" {
			if pg != nil {
				return "", fmt.Errorf("NAS rotation multiple databases refused")
			}
			pg = c
		}
	}
	if pg == nil {
		return "", fmt.Errorf("NAS rotation existing PostgreSQL required; never create an empty database")
	}
	if err = e.owned(r, *pg); err != nil {
		return "", err
	}
	found := false
	for _, m := range pg.Mounts {
		if m.Destination == "/var/lib/postgresql/data" {
			found = m.Source == filepath.Join(r.data, "data/postgres") && m.RW
		}
	}
	if !found || pg.State.Paused || pg.State.Restarting {
		return "", fmt.Errorf("NAS rotation database mount or state refused")
	}
	if !pg.State.Running {
		return "", fmt.Errorf("NAS rotation database stopped; observer never starts it")
	}
	deadline := time.Now().Add(120 * time.Second)
	for {
		c, err := e.inspect(pg.ID)
		if err != nil || c.Image != pg.Image {
			return "", fmt.Errorf("NAS rotation database instance changed")
		}
		if c.State.Running && c.State.Health.Status == "healthy" {
			break
		}
		if r.readyOnly {
			return "", fmt.Errorf("NAS rotation PostgreSQL not ready; observer does not wait or start it")
		}
		if !time.Now().Before(deadline) {
			return "", fmt.Errorf("NAS rotation database not healthy")
		}
		time.Sleep(200 * time.Millisecond)
	}
	b, err := e.call("GET", "/networks/"+nasRotationProject+"_internal", nil)
	if err != nil {
		return "", err
	}
	var network struct {
		ID           string `json:"Id"`
		Name, Driver string
		Internal     bool
		Labels       map[string]string
	}
	if json.Unmarshal(b, &network) != nil || !nasRotationDigest.MatchString(network.ID) || network.Name != nasRotationProject+"_internal" || network.Driver != "bridge" || !network.Internal || network.Labels["com.docker.compose.project"] != nasRotationProject || network.Labels["com.docker.compose.network"] != "internal" {
		return "", fmt.Errorf("NAS rotation fixed private database network required")
	}
	return network.ID, nil
}
func nasRotationDockerLogs(raw []byte) ([]byte, error) {
	var out []byte
	for len(raw) > 0 {
		if len(raw) < 8 {
			return nil, fmt.Errorf("invalid operator log frame")
		}
		n := int(binary.BigEndian.Uint32(raw[4:8]))
		if n > len(raw)-8 || n > 65536 || len(out)+n > 65536 {
			return nil, fmt.Errorf("operator output exceeded bound")
		}
		if raw[0] == 1 {
			out = append(out, raw[8:8+n]...)
		}
		raw = raw[8+n:]
	}
	return bytes.TrimSpace(out), nil
}
func (e *nasRotationEngine) operator(r *nasRotation, action, work string, plan nasRotationPlan) ([]byte, error) {
	switch action {
	case "verify", "check-private", "preflight", "status", "publish", "install", "preflight-recovery", "recover-expired":
	default:
		return nil, fmt.Errorf("unsupported production operator action")
	}
	path := filepath.Join(work, action+"-plan.json")
	if err := nasRotationWriteJSON(path, plan); err != nil {
		return nil, err
	}
	loader := filepath.Join(work, action+"-loader.properties")
	if err := nasRotationWrite(loader, nil); err != nil {
		return nil, err
	}
	network := "none"
	if action == "status" || action == "preflight" || action == "publish" || action == "install" || action == "preflight-recovery" || action == "recover-expired" {
		var err error
		network, err = e.database(r)
		if err != nil {
			return nil, err
		}
	}
	mount := func(path string, ro bool) map[string]any {
		return map[string]any{"Type": "bind", "Source": path, "Target": path, "ReadOnly": ro}
	}
	mounts := []any{mount(r.data, true), mount(work, false), mount(filepath.Dir(r.state), true)}
	if action == "publish" || action == "install" || action == "recover-expired" {
		mounts[2] = mount(filepath.Dir(r.state), false)
	}
	if r.inputDir != "" && !strings.HasPrefix(r.inputDir, r.data+"/") {
		mounts = append(mounts, mount(r.inputDir, true))
	}
	classpath := "/app/platform.jar"
	if r.binding.Jar != "" {
		if err := nasRotationJar(r.recipe, r.binding.Jar); err != nil {
			return nil, err
		}
		classpath = "/opt/auriga/key-operator.jar"
		mounts = append(mounts, map[string]any{"Type": "bind", "Source": filepath.Join(r.recipe, "bin/collab-key-operator.jar"), "Target": classpath, "ReadOnly": true})
	}
	location := (&url.URL{Scheme: "file", Path: loader}).String()
	helperImage := r.binding.Image
	if r.binding.Jar != "" && r.controlImage != "" {
		helperImage = r.controlImage
	}
	body := map[string]any{"Image": helperImage, "User": "0:0", "WorkingDir": "/app", "Entrypoint": []string{"java"}, "Cmd": []string{"-Dloader.main=auriga.collab.network.keys.NetworkKeyOperator", "-Dloader.config.location=" + location, "-cp", classpath, "org.springframework.boot.loader.launch.PropertiesLauncher", action, path}, "Env": []string{"LANG=C.UTF-8"}, "Labels": map[string]string{"auriga.nas.key-operator": "true"}, "HostConfig": map[string]any{"NetworkMode": network, "ReadonlyRootfs": true, "CapDrop": []string{"ALL"}, "CapAdd": []string{"DAC_OVERRIDE", "FOWNER"}, "SecurityOpt": []string{"no-new-privileges:true"}, "Mounts": mounts, "Tmpfs": map[string]string{"/tmp": "mode=1777,size=67108864"}, "Memory": 536870912, "NanoCpus": 1000000000, "PidsLimit": 256}}
	b, err := e.call("POST", "/containers/create", body)
	var created struct {
		ID string `json:"Id"`
	}
	if err != nil || json.Unmarshal(b, &created) != nil || !nasRotationDigest.MatchString(created.ID) {
		return nil, fmt.Errorf("NAS rotation operator helper creation failed")
	}
	defer func() { _, _ = e.call("DELETE", "/containers/"+created.ID+"?force=true&v=true", nil) }()
	if _, err = e.call("POST", "/containers/"+created.ID+"/start", nil); err != nil {
		return nil, err
	}
	b, err = e.call("POST", "/containers/"+created.ID+"/wait?condition=not-running", nil)
	var exit struct {
		Status *int `json:"StatusCode"`
	}
	if err != nil || json.Unmarshal(b, &exit) != nil || exit.Status == nil {
		return nil, fmt.Errorf("NAS rotation operator completion missing")
	}
	if err = nasRotationWriteJSON(filepath.Join(work, action+"-result.json"), map[string]any{"action": action, "image": helperImage, "operatorJarSha256": r.binding.Jar, "exitCode": *exit.Status}); err != nil {
		return nil, err
	}
	if *exit.Status != 0 {
		return nil, fmt.Errorf("NAS rotation production operator rejected %s", action)
	}
	b, err = e.call("GET", "/containers/"+created.ID+"/logs?stdout=true&stderr=false", nil)
	if err != nil {
		return nil, err
	}
	output, err := nasRotationDockerLogs(b)
	if err != nil {
		return nil, err
	}
	if action == "verify" || action == "check-private" || action == "preflight" || action == "preflight-recovery" {
		if !bytes.Equal(output, []byte(`{}`)) {
			return nil, fmt.Errorf("NAS rotation operator verification response refused")
		}
	}
	return output, nil
}

func nasRotationOpen(data, recipe, image, jar string, create bool) (*nasRotation, error) {
	if os.Geteuid() != 0 || !filepath.IsAbs(data) || filepath.Clean(data) != data || !filepath.IsAbs(recipe) || filepath.Clean(recipe) != recipe || strings.ContainsAny(data+recipe, "\\\r\n,$'") {
		return nil, fmt.Errorf("NAS rotation requires root and canonical installation paths")
	}
	if create && (!strings.HasPrefix(image, "sha256:") || !nasRotationDigest.MatchString(strings.TrimPrefix(image, "sha256:"))) {
		return nil, fmt.Errorf("NAS rotation approved image required before storage creation")
	}
	base := filepath.Join(data, "config/materials")
	source := filepath.Join(data, "config/collab-source")
	baseHash, err := verifyNASCollabBase(base)
	if err != nil {
		return nil, err
	}
	if _, err = checkNASCollabSource(source, baseHash); err != nil {
		return nil, err
	}
	raw, err := nasRotationRead(filepath.Join(source, "activation.receipt"), 0)
	if err != nil {
		return nil, err
	}
	r := &nasRotation{data: data, recipe: recipe, source: source, state: filepath.Join(data, "config/collab-runtime/state/state.json"), root: filepath.Join(data, "config/collab-operations")}
	if jar != "" {
		if err = nasRotationJar(recipe, jar); err != nil {
			return nil, err
		}
	}
	r.controlImage = image
	r.binding = nasRotationBinding{Base: baseHash, Source: nasRotationHash(raw), Image: image, Jar: jar, Data: data, Recipe: recipe}
	if _, err = os.Lstat(r.root); os.IsNotExist(err) {
		if !create {
			return r, nil
		}
		if err = checkNASCollabParent(filepath.Dir(r.root)); err != nil {
			return nil, err
		}
		for _, dir := range []string{r.root, filepath.Join(r.root, "releases"), filepath.Join(r.root, "installs"), filepath.Join(r.root, "requests")} {
			if err = os.Mkdir(dir, 0700); err != nil {
				return nil, err
			}
		}
		if err = nasRotationWriteJSON(filepath.Join(r.root, "binding.json"), r.binding); err != nil {
			return nil, err
		}
	}
	if err = nasRotationDirectory(r.root, 0); err != nil {
		return nil, err
	}
	for _, dir := range []string{"releases", "installs", "requests"} {
		if err = nasRotationDirectory(filepath.Join(r.root, dir), 0); err != nil {
			return nil, err
		}
	}
	raw, err = nasRotationRead(filepath.Join(r.root, "binding.json"), 0)
	var binding nasRotationBinding
	if err != nil || nasRotationDecode(raw, &binding) != nil {
		return nil, fmt.Errorf("NAS rotation binding refused")
	}
	if image == "" {
		r.binding.Image = binding.Image
	}
	if jar == "" {
		r.binding.Jar = binding.Jar
	}
	historical, err := nasRotationHistoricalBinding(binding, r.binding)
	if err != nil || !strings.HasPrefix(binding.Image, "sha256:") || !nasRotationDigest.MatchString(strings.TrimPrefix(binding.Image, "sha256:")) {
		return nil, fmt.Errorf("NAS rotation installation binding changed")
	}
	r.binding = historical
	if r.binding.Jar != "" {
		if err = nasRotationJar(recipe, r.binding.Jar); err != nil {
			return nil, err
		}
	}
	return r, nil
}
func nasRotationLocked(r *nasRotation, body func() error) error {
	fd, err := unix.Open(filepath.Join(r.root, ".lock"), unix.O_RDWR|unix.O_CREAT|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		return err
	}
	defer func() { _ = unix.Close(fd) }()
	var st unix.Stat_t
	if unix.Fstat(fd, &st) != nil || st.Uid != 0 || st.Gid != 0 || st.Mode&07777 != 0600 || st.Nlink != 1 || st.Mode&unix.S_IFMT != unix.S_IFREG {
		return fmt.Errorf("NAS rotation lock metadata refused")
	}
	wait := r.lockWait
	if wait == 0 {
		wait = 5 * time.Minute
	}
	deadline := time.Now().Add(wait)
	for unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB) != nil {
		if !time.Now().Before(deadline) {
			return fmt.Errorf("NAS rotation lock wait deadline exceeded")
		}
		time.Sleep(20 * time.Millisecond)
	}
	defer func() { _ = unix.Flock(fd, unix.LOCK_UN) }()
	if _, err = os.Lstat(filepath.Join(r.root, ".pending")); !r.allowPending && !os.IsNotExist(err) {
		return fmt.Errorf("NAS rotation interrupted; preserve pending evidence")
	}
	return body()
}
func nasRotationEngineOpen() (*nasRotationEngine, error) {
	t := &http.Transport{Proxy: nil, DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "unix", "/var/run/docker.sock")
	}}
	e := &nasRotationEngine{client: &http.Client{Transport: t}}
	host, err := os.Hostname()
	if err == nil && len(host) >= 12 && len(host) <= 64 {
		c, err := e.inspect(host)
		if err == nil && c.Config.Labels["auriga.nas.rotation.controller"] == "true" {
			e.self = c.ID
		}
	}
	return e, nil
}
func nasRotationCommand(args []string, stdout io.Writer) error {
	f := flag.NewFlagSet("collab-rotation", flag.ContinueOnError)
	data := f.String("data", "", "fixed NAS data root")
	recipe := f.String("recipe", "", "fixed NAS recipe")
	image := f.String("image", "", "approved immutable local platform image")
	jar := f.String("operator-jar-sha256", "", "independently approved fixed offline operator jar digest")
	count := f.Int("count", 14, "weekly queue count, 1..28")
	input := f.String("input", "", "approved directory")
	if len(args) == 0 {
		return fmt.Errorf("NAS rotation action required")
	}
	action := args[0]
	if err := f.Parse(args[1:]); err != nil {
		return err
	}
	if f.NArg() != 0 {
		return fmt.Errorf("unexpected NAS rotation arguments")
	}
	switch action {
	case "publish", "install", "status", "history-check", "history-check-pg", "startup-check", "path", "check", "stop", "prepare", "renew", "prepare-recovery", "recover-expired", "queue-prepare", "queue-import", "queue-export", "recover-pending", "pause", "resume":
	default:
		return fmt.Errorf("unsupported NAS rotation action")
	}
	create := action == "publish" || action == "prepare" || action == "renew" || action == "prepare-recovery" || action == "recover-expired" || action == "queue-prepare"
	if action == "path" || action == "check" {
		if os.Geteuid() != 0 {
			return fmt.Errorf("NAS rotation requires Linux installer root")
		}
		if _, err := os.Lstat(filepath.Join(*data, "config/collab-operations")); os.IsNotExist(err) {
			return nil
		}
	}
	var engine *nasRotationEngine
	if action != "path" && action != "history-check" {
		var err error
		engine, err = nasRotationEngineOpen()
		if err != nil {
			return err
		}
		defer engine.client.CloseIdleConnections()
		if *image != "" {
			if !regexp.MustCompile(`^(sha256:|[^\s]+@sha256:)[a-f0-9]{64}$`).MatchString(*image) {
				return fmt.Errorf("NAS rotation immutable local image required")
			}
			b, err := engine.call("GET", "/images/"+url.PathEscape(*image)+"/json", nil)
			var info struct {
				ID           string `json:"Id"`
				OS           string `json:"Os"`
				Architecture string
			}
			if err != nil || json.Unmarshal(b, &info) != nil || !strings.HasPrefix(info.ID, "sha256:") || !nasRotationDigest.MatchString(strings.TrimPrefix(info.ID, "sha256:")) || info.OS != "linux" || info.Architecture != "amd64" {
				return fmt.Errorf("NAS rotation approved image unavailable")
			}
			*image = info.ID
		}
	}
	openImage := *image
	if action == "history-check" {
		openImage = ""
	} // offline: retain the archived image binding
	r, err := nasRotationOpen(*data, *recipe, openImage, *jar, create)
	if err != nil {
		return err
	}
	if _, err = os.Lstat(r.root); os.IsNotExist(err) {
		if action == "startup-check" {
			if err = r.historyCheck(nil, io.Discard); err != nil {
				return err
			}
			_, err = engine.database(r)
			return err
		}
		if action == "path" || action == "check" {
			return nil
		}
		return fmt.Errorf("NAS rotation not initialized")
	}
	r.inspectOnly = action == "path" || action == "status" || action == "history-check" || action == "history-check-pg"
	r.readyOnly = action == "check" || action == "history-check-pg"
	if action == "startup-check" {
		r.lockWait = 30 * time.Second
	}
	if action == "path" || action == "check" || action == "history-check" || action == "pause" || action == "resume" {
		r.lockWait = time.Nanosecond // optional message operations must not hold up core startup
	}
	r.allowPending = r.inspectOnly || action == "check" || action == "recover-pending" || action == "pause" || action == "resume" || action == "stop"
	r.recovery = action == "recover-expired" || action == "prepare-recovery"
	if r.recovery && r.binding.Jar == "" {
		return fmt.Errorf("expired recovery requires independently approved operator jar")
	}
	if action == "publish" || action == "install" || action == "recover-expired" || action == "queue-import" {
		if !filepath.IsAbs(*input) || filepath.Clean(*input) != *input {
			return fmt.Errorf("NAS rotation approved canonical input required")
		}
		r.inputDir = *input
	} else if *input != "" {
		return fmt.Errorf("NAS rotation unexpected input")
	}
	if action != "path" && action != "history-check" {
		r.engine = engine
		r.operator = func(action, work string, p nasRotationPlan) ([]byte, error) {
			return r.engine.operator(r, action, work, p)
		}
	}
	if action == "stop" {
		r.stopOnly = true
		return nasRotationLocked(r, func() error {
			if err := r.schedulerControl("pause"); err != nil {
				return err
			}
			return r.engine.stop(r)
		})
	}
	return nasRotationLocked(r, func() error {
		rows, err := r.releases()
		if err != nil {
			return err
		}
		switch action {
		case "publish", "recover-expired":
			return r.publish()
		case "install":
			return r.install()
		case "status":
			s, err := r.status(rows)
			if err != nil {
				return err
			}
			return json.NewEncoder(stdout).Encode(s)
		case "history-check":
			return r.historyCheck(rows, stdout)
		case "history-check-pg":
			return r.historyCheckPG(rows, stdout)
		case "pause", "resume":
			return r.schedulerControl(action)
		case "queue-export":
			return r.queueExport(stdout)
		case "queue-prepare":
			return r.queuePrepare(rows, *count, stdout)
		case "queue-import":
			return r.queueImport(stdout)
		case "recover-pending":
			return r.recoverPending(rows, stdout)
		case "renew":
			return r.queueTick(rows, stdout)
		case "prepare", "prepare-recovery":
			return r.prepare(rows, stdout)
		case "check":
			if err = r.available(rows, time.Now()); err != nil {
				return err
			}
			_, err = r.status(rows)
			return err
		case "startup-check":
			if err = r.historyCheck(rows, io.Discard); err != nil {
				return err
			}
			if err = r.schedulerControl("resume"); err != nil {
				return err
			}
			if err = r.available(rows, time.Now()); err != nil {
				return err
			}
			_, err = r.status(rows) // waits only for a PG that is already running
			return err
		case "path":
			dir, err := r.runtimePath(rows)
			if err != nil {
				return err
			}
			_, err = fmt.Fprint(stdout, dir)
			return err
		}
		return fmt.Errorf("NAS rotation action refused")
	})
}

func nasRotationJar(recipe, expected string) error {
	if !nasRotationDigest.MatchString(expected) {
		return fmt.Errorf("invalid approved operator jar digest")
	}
	path := filepath.Join(recipe, "bin/collab-key-operator.jar")
	for p := path; ; p = filepath.Dir(p) {
		s, e := os.Lstat(p)
		if e != nil || s.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("operator jar path refused")
		}
		if p == filepath.Dir(p) {
			break
		}
	}
	var st unix.Stat_t
	if unix.Lstat(path, &st) != nil || st.Uid != 0 || st.Gid != 0 || st.Nlink != 1 || st.Mode&unix.S_IFMT != unix.S_IFREG || st.Mode&0022 != 0 || st.Size < 1 || st.Size > 256<<20 {
		return fmt.Errorf("operator jar metadata refused")
	}
	f, e := os.OpenFile(path, os.O_RDONLY|unix.O_NOFOLLOW, 0)
	if e != nil {
		return e
	}
	defer func() { _ = f.Close() }()
	before, e := f.Stat()
	if e != nil {
		return e
	}
	h := sha256.New()
	n, e := io.Copy(h, io.LimitReader(f, 256<<20+1))
	after, se := os.Lstat(path)
	if e != nil || se != nil || n != before.Size() || !os.SameFile(before, after) || !before.ModTime().Equal(after.ModTime()) || fmt.Sprintf("%x", h.Sum(nil)) != expected {
		return fmt.Errorf("operator jar bytes changed")
	}
	return nil
}
