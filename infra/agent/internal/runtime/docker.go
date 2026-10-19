package runtime

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/hansjone/auriga/infra/agent/internal/policy"
	"github.com/hansjone/auriga/infra/agent/internal/renderer"
	"github.com/hansjone/auriga/infra/agent/internal/storage"
)

const (
	labelInstance   = "auriga.instance"
	labelGeneration = "auriga.generation"
	labelManaged    = "auriga.managed"
)

// Docker implements Driver via the Docker Engine API (also works with Podman).
type Docker struct {
	Client *EngineClient
	Policy policy.Policy
	// DataRoot is used only for default bind paths when Spec.HomeBind is empty.
	DataRoot string

	mu        sync.Mutex
	endpoints map[string]string
	ports     map[string]uint32
}

// DockerConfig configures the Docker driver.
type DockerConfig struct {
	Host     string
	DataRoot string
	Timeout  time.Duration
	Policy   policy.Policy
}

// NewDocker dials the Engine API.
// Zero-value Policy uses deny-by-default Default(); AllowAnyImage is never implied.
func NewDocker(cfg DockerConfig) (*Docker, error) {
	host := cfg.Host
	if host == "" {
		host = os.Getenv("DOCKER_HOST")
	}
	client, err := DialEngine(host, cfg.Timeout)
	if err != nil {
		return nil, err
	}
	d := NewDockerWithClient(client, cfg.DataRoot)
	d.Policy = normalizeDockerPolicy(cfg.Policy)
	return d, nil
}

// NewDockerWithClient wraps an existing EngineClient (tests / custom dial).
// Policy defaults to deny-all images; tests must set AllowAnyImage or AllowedImageRepos explicitly.
func NewDockerWithClient(client *EngineClient, dataRoot string) *Docker {
	return &Docker{
		Client:    client,
		DataRoot:  dataRoot,
		Policy:    policy.Default(),
		endpoints: make(map[string]string),
		ports:     make(map[string]uint32),
	}
}

// normalizeDockerPolicy fills resource caps from Default when zero.
// AllowAnyImage stays false unless the caller set it (tests only).
func normalizeDockerPolicy(p policy.Policy) policy.Policy {
	def := policy.Default()
	if p.MaxCPUMillis <= 0 {
		p.MaxCPUMillis = def.MaxCPUMillis
	}
	if p.MaxMemoryMB <= 0 {
		p.MaxMemoryMB = def.MaxMemoryMB
	}
	if p.MaxPIDs <= 0 {
		p.MaxPIDs = def.MaxPIDs
	}
	if p.MaxArtifactBytes <= 0 {
		p.MaxArtifactBytes = def.MaxArtifactBytes
	}
	if p.ArtifactConnectTimeout <= 0 {
		p.ArtifactConnectTimeout = def.ArtifactConnectTimeout
	}
	if p.ArtifactTotalTimeout <= 0 {
		p.ArtifactTotalTimeout = def.ArtifactTotalTimeout
	}
	p.AllowedArtifactURLPrefixes = policy.NormalizeURLPrefixes(p.AllowedArtifactURLPrefixes)
	return p
}

func (d *Docker) Name() string { return "docker" }

func (d *Docker) Capabilities() Caps {
	return Caps{NetworkIsolation: true, DiskQuota: false, ReadOnlyRoot: true}
}

// Ensure creates or replaces the container definition without starting it.
func (d *Docker) Ensure(ctx context.Context, s *Spec, r *Rendered) error {
	if s == nil {
		return fmt.Errorf("docker: missing spec")
	}
	if err := storage.ValidateInstanceID(s.InstanceID); err != nil {
		return fmt.Errorf("docker: %w", err)
	}
	if s.Image == "" {
		return fmt.Errorf("docker: image required (repo@sha256:…)")
	}
	if err := d.Policy.AllowImage(s.Image); err != nil {
		return err
	}
	if s.HomeBind == "" {
		return fmt.Errorf("docker: HomeBind required")
	}
	if s.MetaBind == "" {
		return fmt.Errorf("docker: MetaBind required")
	}
	name := containerName(s.InstanceID)
	netName := networkName(s.InstanceID)
	uid := s.HostUID
	if uid < 200000 {
		return fmt.Errorf("docker: HostUID must be allocated (>=200000)")
	}
	port := s.ListenPort
	if port == 0 {
		port = 7860
	}

	cpu, mem, pids := d.Policy.ClampResources(s.CPUMillis, s.MemoryMB, s.PIDs)
	s.CPUMillis, s.MemoryMB, s.PIDs = cpu, mem, pids

	if err := d.Client.PullImage(ctx, s.Image); err != nil {
		return err
	}
	if _, err := d.Client.NetworkCreate(ctx, netName, true, map[string]string{
		labelInstance: s.InstanceID,
		labelManaged:  "true",
	}); err != nil {
		return err
	}

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

	env := make([]string, 0, len(s.Env))
	for k, v := range s.Env {
		env = append(env, k+"="+v)
	}

	memBytes := mem * 1024 * 1024
	nanoCPUs := cpu * 1_000_000

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
			"Mounts": []map[string]any{
				{
					"Type":   "bind",
					"Source": s.HomeBind,
					"Target": "/home/dsh",
					"RW":     true,
				},
				{
					"Type":     "bind",
					"Source":   s.MetaBind,
					"Target":   renderer.ContainerMetaRoot,
					"ReadOnly": true,
					"RW":       false,
				},
			},
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
	d.mu.Unlock()
	_ = r
	return nil
}

func (d *Docker) Start(ctx context.Context, id string) error {
	if err := storage.ValidateInstanceID(id); err != nil {
		return err
	}
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
	if err := storage.ValidateInstanceID(id); err != nil {
		return err
	}
	sec := int(grace.Seconds())
	if sec < 1 {
		sec = 5
	}
	return d.Client.ContainerStop(ctx, containerName(id), sec)
}

func (d *Docker) Remove(ctx context.Context, id string) error {
	if err := storage.ValidateInstanceID(id); err != nil {
		return err
	}
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
	if err := storage.ValidateInstanceID(id); err != nil {
		return Status{}, err
	}
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
	if err := storage.ValidateInstanceID(id); err != nil {
		return Usage{}, err
	}
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

// containerName uses the validated instance id verbatim (no truncation/mutation).
func containerName(instanceID string) string {
	return "auriga-inst-" + instanceID
}

func networkName(instanceID string) string {
	return "auriga-net-" + instanceID
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
