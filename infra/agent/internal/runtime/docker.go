package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	labelInstance   = "auriga.instance"
	labelGeneration = "auriga.generation"
	labelManaged    = "auriga.managed"
	uidBase         = 200000
	uidSpan         = 100000
)

// Docker implements Driver via the Docker Engine API (also works with Podman).
type Docker struct {
	Client *EngineClient
	// DataRoot is used only for default bind paths when Spec.HomeBind is empty.
	DataRoot string

	mu        sync.Mutex
	endpoints map[string]string // instance id → local URL
	ports     map[string]uint32
}

// DockerConfig configures the Docker driver.
type DockerConfig struct {
	Host     string // DOCKER_HOST style; empty = platform default
	DataRoot string
	Timeout  time.Duration
}

// NewDocker dials the Engine API.
func NewDocker(cfg DockerConfig) (*Docker, error) {
	host := cfg.Host
	if host == "" {
		host = os.Getenv("DOCKER_HOST")
	}
	client, err := DialEngine(host, cfg.Timeout)
	if err != nil {
		return nil, err
	}
	return NewDockerWithClient(client, cfg.DataRoot), nil
}

// NewDockerWithClient wraps an existing EngineClient (tests / custom dial).
func NewDockerWithClient(client *EngineClient, dataRoot string) *Docker {
	return &Docker{
		Client:    client,
		DataRoot:  dataRoot,
		endpoints: make(map[string]string),
		ports:     make(map[string]uint32),
	}
}

func (d *Docker) Name() string { return "docker" }

func (d *Docker) Capabilities() Caps {
	return Caps{NetworkIsolation: true, DiskQuota: false, ReadOnlyRoot: true}
}

// Ensure creates or replaces the container definition without starting it.
func (d *Docker) Ensure(ctx context.Context, s *Spec, r *Rendered) error {
	if s == nil || s.InstanceID == "" {
		return fmt.Errorf("docker: missing instance id")
	}
	if s.Image == "" {
		return fmt.Errorf("docker: image required (repo@sha256:…)")
	}
	if !strings.Contains(s.Image, "@sha256:") {
		return fmt.Errorf("docker: image must be pinned by digest")
	}
	name := containerName(s.InstanceID)
	netName := networkName(s.InstanceID)
	uid := s.HostUID
	if uid == 0 {
		uid = HostUIDFor(s.InstanceID)
	}
	home := s.HomeBind
	if home == "" {
		return fmt.Errorf("docker: HomeBind required")
	}
	port := s.ListenPort
	if port == 0 {
		port = 7860
	}

	if err := d.Client.PullImage(ctx, s.Image); err != nil {
		return err
	}
	if _, err := d.Client.NetworkCreate(ctx, netName, true, map[string]string{
		labelInstance: s.InstanceID,
		labelManaged:  "true",
	}); err != nil {
		return err
	}

	// Replace existing container with same name.
	_ = d.Client.ContainerStop(ctx, name, 5)
	_ = d.Client.ContainerRemove(ctx, name, true)

	labels := map[string]string{
		labelInstance:   s.InstanceID,
		labelGeneration: strconv.FormatInt(s.Generation, 10),
		labelManaged:    "true",
	}
	for k, v := range s.Labels {
		labels["auriga.label."+k] = v
	}

	env := make([]string, 0, len(s.Env)+2)
	for k, v := range s.Env {
		env = append(env, k+"="+v)
	}

	memBytes := s.MemoryMB * 1024 * 1024
	if memBytes <= 0 {
		memBytes = 2048 * 1024 * 1024
	}
	nanoCPUs := s.CPUMillis * 1_000_000 // 1000 millis = 1 CPU = 1e9 NanoCPUs
	if nanoCPUs <= 0 {
		nanoCPUs = 1000 * 1_000_000
	}
	pids := s.PIDs
	if pids <= 0 {
		pids = 512
	}

	body := map[string]any{
		"Image":  s.Image,
		"User":   fmt.Sprintf("%d:%d", uid, uid),
		"Env":    env,
		"Labels": labels,
		"ExposedPorts": map[string]any{
			fmt.Sprintf("%d/tcp", port): map[string]any{},
		},
		"HostConfig": map[string]any{
			"ReadonlyRootfs": true,
			"CapDrop":        []string{"ALL"},
			"SecurityOpt":    []string{"no-new-privileges:true"},
			"Privileged":     false,
			"Memory":         memBytes,
			"NanoCpus":       nanoCPUs,
			"PidsLimit":      pids,
			"NetworkMode":    netName,
			"Binds":          []string{home + ":/home/dsh:rw"},
			"Tmpfs": map[string]string{
				"/tmp": "rw,noexec,nosuid,size=64m",
			},
			"PortBindings": map[string]any{
				fmt.Sprintf("%d/tcp", port): []map[string]string{
					{"HostIp": "127.0.0.1", "HostPort": "0"},
				},
			},
		},
		"NetworkingConfig": map[string]any{
			"EndpointsConfig": map[string]any{
				netName: map[string]any{},
			},
		},
	}
	if _, err := d.Client.ContainerCreate(ctx, name, body); err != nil {
		return err
	}
	d.mu.Lock()
	d.ports[s.InstanceID] = port
	if r != nil && r.Digest != "" {
		// digest recorded by caller; nothing to store here
	}
	d.mu.Unlock()
	return nil
}

func (d *Docker) Start(ctx context.Context, id string) error {
	name := containerName(id)
	if err := d.Client.ContainerStart(ctx, name); err != nil {
		return err
	}
	insp, err := d.Client.ContainerInspect(ctx, name)
	if err != nil {
		return err
	}
	hostPort := publishedPort(insp, d.portOf(id))
	d.mu.Lock()
	d.endpoints[id] = fmt.Sprintf("http://127.0.0.1:%s", hostPort)
	d.mu.Unlock()
	return nil
}

func (d *Docker) Stop(ctx context.Context, id string, grace time.Duration) error {
	sec := int(grace.Seconds())
	if sec < 1 {
		sec = 5
	}
	return d.Client.ContainerStop(ctx, containerName(id), sec)
}

func (d *Docker) Remove(ctx context.Context, id string) error {
	_ = d.Client.ContainerStop(ctx, containerName(id), 5)
	if err := d.Client.ContainerRemove(ctx, containerName(id), true); err != nil {
		return err
	}
	_ = d.Client.NetworkRemove(ctx, networkName(id))
	d.mu.Lock()
	delete(d.endpoints, id)
	delete(d.ports, id)
	d.mu.Unlock()
	return nil
}

func (d *Docker) Inspect(ctx context.Context, id string) (Status, error) {
	insp, err := d.Client.ContainerInspect(ctx, containerName(id))
	if err != nil {
		return Status{InstanceID: id}, err
	}
	if insp == nil {
		return Status{InstanceID: id}, nil
	}
	st := Status{InstanceID: id}
	if state, ok := insp["State"].(map[string]any); ok {
		st.Running, _ = state["Running"].(bool)
		st.Ready = st.Running
		if !st.Running {
			if errMsg, _ := state["Error"].(string); errMsg != "" {
				st.Error = errMsg
			}
		}
	}
	if cfg, ok := insp["Config"].(map[string]any); ok {
		if labels, ok := cfg["Labels"].(map[string]any); ok {
			if g, ok := labels[labelGeneration].(string); ok {
				st.Generation, _ = strconv.ParseInt(g, 10, 64)
			}
		}
	}
	return st, nil
}

func (d *Docker) Stats(ctx context.Context, id string) (Usage, error) {
	raw, err := d.Client.ContainerStats(ctx, containerName(id))
	if err != nil || raw == nil {
		return Usage{}, err
	}
	var u Usage
	if mem, ok := raw["memory_stats"].(map[string]any); ok {
		if usage, ok := mem["usage"].(float64); ok {
			u.MemoryMB = int64(usage) / (1024 * 1024)
		}
	}
	if pids, ok := raw["pids_stats"].(map[string]any); ok {
		if cur, ok := pids["current"].(float64); ok {
			u.PIDs = int64(cur)
		}
	}
	return u, nil
}

func (d *Docker) Endpoint(id string) (Endpoint, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	u := d.endpoints[id]
	if u == "" {
		return Endpoint{}, fmt.Errorf("docker: no endpoint for %s", id)
	}
	return Endpoint{URL: u}, nil
}

func (d *Docker) List(ctx context.Context) ([]Status, error) {
	list, err := d.Client.ContainerList(ctx, true, map[string][]string{
		"label": {labelManaged + "=true"},
	})
	if err != nil {
		return nil, err
	}
	out := make([]Status, 0, len(list))
	for _, c := range list {
		labels, _ := c["Labels"].(map[string]any)
		id, _ := labels[labelInstance].(string)
		if id == "" {
			continue
		}
		st := Status{InstanceID: id}
		if g, ok := labels[labelGeneration].(string); ok {
			st.Generation, _ = strconv.ParseInt(g, 10, 64)
		}
		if state, ok := c["State"].(string); ok {
			st.Running = state == "running"
			st.Ready = st.Running
		}
		out = append(out, st)
	}
	return out, nil
}

func (d *Docker) portOf(id string) uint32 {
	d.mu.Lock()
	defer d.mu.Unlock()
	if p := d.ports[id]; p != 0 {
		return p
	}
	return 7860
}

// HostUIDFor maps an instance id to a stable host uid in [200000, 299999].
func HostUIDFor(instanceID string) int {
	sum := sha256.Sum256([]byte(instanceID))
	n := binary.BigEndian.Uint32(sum[:4]) % uidSpan
	return uidBase + int(n)
}

func containerName(instanceID string) string {
	return "auriga-inst-" + sanitizeID(instanceID)
}

func networkName(instanceID string) string {
	return "auriga-net-" + sanitizeID(instanceID)
}

func sanitizeID(id string) string {
	var b strings.Builder
	for _, r := range id {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			b.WriteRune(r)
		} else {
			b.WriteByte('-')
		}
	}
	s := b.String()
	if len(s) > 60 {
		s = s[:60]
	}
	return s
}

func publishedPort(insp map[string]any, containerPort uint32) string {
	net, _ := insp["NetworkSettings"].(map[string]any)
	if net == nil {
		return "0"
	}
	ports, _ := net["Ports"].(map[string]any)
	key := fmt.Sprintf("%d/tcp", containerPort)
	arr, _ := ports[key].([]any)
	if len(arr) == 0 {
		return "0"
	}
	first, _ := arr[0].(map[string]any)
	hp, _ := first["HostPort"].(string)
	if hp == "" {
		return "0"
	}
	return hp
}
