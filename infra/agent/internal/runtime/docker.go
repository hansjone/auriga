package runtime

import (
	"context"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hansjone/auriga/infra/agent/internal/policy"
	"github.com/hansjone/auriga/infra/agent/internal/renderer"
	"github.com/hansjone/auriga/infra/agent/internal/state"
	"github.com/hansjone/auriga/infra/agent/internal/storage"
	nodev1 "github.com/hansjone/auriga/infra/gen/auriga/node/v1"
	"google.golang.org/protobuf/proto"
)

const (
	labelInstance   = "auriga.instance"
	labelGeneration = "auriga.generation"
	labelManaged    = "auriga.managed"
)

// Docker implements Driver via the Docker Engine API (also works with Podman).
type Docker struct {
	Activation *ReleaseActivation
	Client     *EngineClient
	Policy     policy.Policy
	// DataRoot is used only for default bind paths when Spec.HomeBind is empty.
	DataRoot      string
	EdgeContainer string
	PlatformHost  string
	PortalHost    string

	mu              sync.Mutex
	endpoints       map[string]string
	endpointRevoker func(string)
}

// DockerConfig configures the Docker driver.
type DockerConfig struct {
	Host     string
	DataRoot string
	Timeout  time.Duration
	Policy   policy.Policy
	// Trusted node configuration, never supplied by an instance spec. The edge
	// joins each isolated instance network with this platform DNS alias.
	EdgeContainer string
	PlatformHost  string
	// Optional trusted HTTPS portal DNS alias for managed device traffic.
	PortalHost string
}

// NewDocker dials the Engine API.
// Zero-value Policy uses deny-by-default Default(); AllowAnyImage is never implied.
func NewDocker(cfg DockerConfig) (*Docker, error) {
	if err := validateEdgeNetworkConfig(cfg.EdgeContainer, cfg.PlatformHost); err != nil {
		return nil, err
	}
	if cfg.PortalHost != "" {
		if err := validateEdgeNetworkConfig(cfg.EdgeContainer, cfg.PortalHost); err != nil {
			return nil, err
		}
	}
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
	d.EdgeContainer, d.PlatformHost = cfg.EdgeContainer, cfg.PlatformHost
	d.PortalHost = cfg.PortalHost
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
	if s.Generation <= 0 {
		return fmt.Errorf("docker: invalid generation")
	}
	scope, err := requireActivation(ctx, s.InstanceID)
	if err != nil {
		return err
	}
	if d.Activation == nil || scope.coordinator != d.Activation || s.Generation != scope.spec.Generation || s.Full == nil || !proto.Equal(s.Full, scope.spec) || s.Image != scope.spec.Dsh.Image {
		return fmt.Errorf("docker: fixed spec does not match activation")
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

	env := make([]string, 0, len(s.Env))
	for _, k := range sortedEnvKeys(s.Env) {
		v := s.Env[k]
		if err := validateEnvPair(k, v); err != nil {
			return fmt.Errorf("docker: %w", err)
		}
		env = append(env, k+"="+v)
	}

	if err := d.Client.PullImage(ctx, s.Image); err != nil {
		return err
	}
	insp, err := d.Client.ImageInspect(ctx, s.Image)
	if err != nil {
		return err
	}
	_, digest, _ := strings.Cut(s.Image, "@")
	if insp["Os"] != scope.candidate.Partition.OS || insp["Architecture"] != scope.candidate.Partition.Arch || !imageHasRepoDigest(insp, s.Image, digest) {
		return fmt.Errorf("docker: actual image does not match signed OCI evidence")
	}
	engineImageID, _ := insp["Id"].(string)
	if err := scope.pinEngineImage(engineImageID); err != nil {
		return err
	}
	if err := scope.recheck(); err != nil {
		return err
	}
	d.revoke(s.InstanceID)
	if _, err := d.Client.NetworkCreate(ctx, netName, true, map[string]string{
		labelInstance: s.InstanceID,
		labelManaged:  "true",
	}); err != nil {
		return err
	}
	if d.EdgeContainer != "" {
		if err := d.ensureEdgeNetwork(ctx, s.InstanceID); err != nil {
			return err
		}
	}

	// Never adopt or remove a name collision without checking actual ownership.
	old, err := d.Client.ContainerInspect(ctx, name)
	if err != nil {
		return err
	}
	prior, err := d.Activation.Store.GetExecution(s.InstanceID)
	if err != nil {
		return err
	}
	if old != nil && prior == nil {
		return fmt.Errorf("docker: existing container lacks actual execution; protected cleanup required")
	}
	if prior != nil {
		actual, err := d.Client.ContainerInspect(ctx, prior.ContainerID)
		if err != nil {
			return err
		}
		if actual != nil {
			if _, err := d.verifyContainer(ctx, prior); err != nil {
				return err
			}
			if old != nil && old["Id"] != prior.ContainerID {
				return fmt.Errorf("docker: container name was rebound")
			}
			old = actual
		}
	}
	if old != nil {
		cfg, _ := old["Config"].(map[string]any)
		labels, _ := cfg["Labels"].(map[string]any)
		oldID, _ := old["Id"].(string)
		if oldID == "" || labels[labelManaged] != "true" || labels[labelInstance] != s.InstanceID {
			return fmt.Errorf("docker: replacement container ownership mismatch")
		}
		if err := d.Client.ContainerStop(ctx, oldID, 5); err != nil {
			return err
		}
		if err := d.Client.ContainerRemove(ctx, oldID, true); err != nil {
			return err
		}
	}

	labels := map[string]string{
		labelInstance:   s.InstanceID,
		labelGeneration: strconv.FormatInt(s.Generation, 10),
		labelManaged:    "true",
	}
	for k, v := range s.Labels {
		labels["auriga.label."+k] = v
	}

	memBytes := mem * 1024 * 1024
	nanoCPUs := cpu * 1_000_000

	body := map[string]any{
		"Image":  engineImageID,
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
	if err := scope.recheck(); err != nil {
		return err
	}
	containerID, err := d.Client.ContainerCreate(ctx, name, body, scope.candidate.Partition.OS+"/"+scope.candidate.Partition.Arch)
	if err != nil {
		return err
	}
	record := &state.ExecutionRecord{Version: 2, InstanceID: s.InstanceID, Generation: s.Generation, DSH: proto.Clone(scope.spec.Dsh).(*nodev1.DshRuntime), Candidate: scope.candidate, ContainerID: containerID, ConfigID: scope.image.ConfigID(), EngineImageID: engineImageID, ProofDigest: scope.image.ManifestDigest(), Phase: "created"}
	if _, err := d.verifyContainer(ctx, record); err != nil {
		return err
	}
	if err := d.Activation.Store.PutExecution(*record); err != nil {
		return err
	}
	scope.record = record
	_ = r
	return nil
}

func (d *Docker) Start(ctx context.Context, id string) error {
	if err := storage.ValidateInstanceID(id); err != nil {
		return err
	}
	d.revoke(id)
	scope, err := requireActivation(ctx, id)
	if err != nil {
		return err
	}
	if d.Activation == nil || scope.coordinator != d.Activation {
		return fmt.Errorf("docker activation mismatch")
	}
	record, err := d.actualRecord(id)
	if err != nil {
		return err
	}
	if record.Generation != scope.spec.Generation || !executionMatchesImage(record, scope.image, scope.candidate) {
		return fmt.Errorf("docker actual execution generation mismatch")
	}
	if err := scope.pinEngineImage(record.EngineImageID); err != nil {
		return err
	}
	before, err := d.verifyContainer(ctx, record)
	if err != nil {
		return err
	}
	// A replacement edge loses its endpoints on existing instance networks.
	// Restore the trusted bridge before starting or publishing the instance.
	if d.EdgeContainer != "" {
		if err := d.ensureEdgeNetwork(ctx, id); err != nil {
			return err
		}
	}
	if err := scope.recheck(); err != nil {
		return err
	}
	name := record.ContainerID
	liveState, _ := before["State"].(map[string]any)
	if record.Phase != "started" || liveState["Running"] != true {
		if err := d.Client.ContainerStart(ctx, name); err != nil {
			return err
		}
	}
	insp, err := d.verifyContainer(ctx, record)
	if err != nil {
		return err
	}
	startedState, _ := insp["State"].(map[string]any)
	if startedState["Running"] != true {
		return fmt.Errorf("docker: actual container did not remain running")
	}
	port, err := instancePort(insp)
	if err != nil {
		return fmt.Errorf("docker: %s: %w", name, err)
	}
	endpoint, err := instanceEndpoint(insp, networkName(id), port)
	if err != nil {
		return fmt.Errorf("docker: %s: %w", name, err)
	}
	if err := waitInstanceHealth(ctx, endpoint); err != nil {
		return fmt.Errorf("docker: %s health not ready: %w", name, err)
	}
	if err := scope.recheck(); err != nil {
		return err
	}
	final, err := d.verifyContainer(ctx, record)
	if err != nil {
		return err
	}
	finalState, _ := final["State"].(map[string]any)
	if finalState["Running"] != true {
		return fmt.Errorf("docker: actual container exited before Ready")
	}
	record.Phase = "started"
	if err := d.Activation.Store.PutExecution(*record); err != nil {
		return err
	}
	d.mu.Lock()
	// Saving the execution is synchronous and may outlive the Start deadline.
	// Keep that durable process fact, but do not publish an expired readiness proof.
	if err := ctx.Err(); err != nil {
		d.mu.Unlock()
		return err
	}
	d.endpoints[id] = endpoint
	d.mu.Unlock()
	return nil
}

// instanceEndpoint picks how the agent reaches the instance. Docker does not
// publish ports for containers attached only to an internal network (the
// isolation we want), so the usual path is the container address on its own
// network, which the host can still route to; a published loopback port is
// used when present.
func instanceEndpoint(insp map[string]any, network string, port uint32) (string, error) {
	if hp := publishedPort(insp, port); hp != "0" {
		return fmt.Sprintf("http://127.0.0.1:%s", hp), nil
	}
	settings, _ := insp["NetworkSettings"].(map[string]any)
	networks, _ := settings["Networks"].(map[string]any)
	n, _ := networks[network].(map[string]any)
	ip, _ := n["IPAddress"].(string)
	if net.ParseIP(ip) == nil {
		return "", fmt.Errorf("no published port and no address on network %s", network)
	}
	return "http://" + net.JoinHostPort(ip, strconv.FormatUint(uint64(port), 10)), nil
}

func (d *Docker) Stop(ctx context.Context, id string, grace time.Duration) error {
	if err := storage.ValidateInstanceID(id); err != nil {
		return err
	}
	sec := int(grace.Seconds())
	if sec < 1 {
		sec = 5
	}
	d.revoke(id)
	name, err := d.cleanupContainer(ctx, id)
	if err != nil {
		return err
	}
	if name == "" {
		return nil
	}
	return d.Client.ContainerStop(ctx, name, sec)
}

func (d *Docker) Remove(ctx context.Context, id string) error {
	if err := storage.ValidateInstanceID(id); err != nil {
		return err
	}
	d.revoke(id)
	name, err := d.cleanupContainer(ctx, id)
	if err != nil {
		return err
	}
	// Validate ownership before touching an existing network or its edge endpoint.
	// An absent network is harmless (e.g. a retry after successful removal).
	var owned *engineNetwork
	if d.EdgeContainer != "" {
		var err error
		owned, err = d.ownedNetwork(ctx, id)
		if err != nil {
			return err
		}
	}
	if name != "" {
		if err := d.Client.ContainerStop(ctx, name, 5); err != nil {
			return err
		}
		if err := d.Client.ContainerRemove(ctx, name, true); err != nil {
			return err
		}
		if insp, err := d.Client.ContainerInspect(ctx, name); err != nil || insp != nil {
			if err != nil {
				return err
			}
			return fmt.Errorf("docker: removed container still exists")
		}
	}
	if owned != nil {
		if err := d.detachEdge(ctx, owned); err != nil {
			return err
		}
	}
	if err := d.Client.NetworkRemove(ctx, networkName(id)); err != nil {
		return err
	}
	if d.Activation != nil {
		record, err := d.Activation.Store.GetExecution(id)
		if err != nil {
			return err
		}
		if record != nil {
			if err := d.Activation.Store.DeleteExecution(id, record.ContainerID); err != nil {
				return err
			}
		}
	}
	d.mu.Lock()
	delete(d.endpoints, id)
	d.mu.Unlock()
	return nil
}

func (d *Docker) Inspect(ctx context.Context, id string) (Status, error) {
	if err := storage.ValidateInstanceID(id); err != nil {
		return Status{}, err
	}
	if err := ctx.Err(); err != nil {
		d.revoke(id)
		return Status{InstanceID: id}, err
	}
	record, err := d.observationRecord(id)
	if err != nil {
		return Status{InstanceID: id}, err
	}
	insp, err := d.verifyContainer(ctx, record)
	if err != nil {
		d.revoke(id)
		return Status{InstanceID: id}, err
	}
	if insp == nil {
		return Status{InstanceID: id}, nil
	}
	st := Status{InstanceID: id}
	if state, ok := insp["State"].(map[string]any); ok {
		st.Running, _ = state["Running"].(bool)
		_, endpointErr := d.endpoint(ctx, id)
		if err := ctx.Err(); err != nil {
			d.revoke(id)
			return Status{InstanceID: id}, err
		}
		st.Ready = st.Running && endpointErr == nil
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
	if err := ctx.Err(); err != nil {
		d.revoke(id)
		return Status{InstanceID: id}, err
	}
	return st, nil
}

func (d *Docker) Stats(ctx context.Context, id string) (Usage, error) {
	if err := storage.ValidateInstanceID(id); err != nil {
		return Usage{}, err
	}
	record, err := d.observationRecord(id)
	if err != nil {
		return Usage{}, err
	}
	raw, err := d.Client.ContainerStats(ctx, record.ContainerID)
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
	return d.endpoint(context.Background(), id)
}

func (d *Docker) endpoint(ctx context.Context, id string) (Endpoint, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := ctx.Err(); err != nil {
		d.revoke(id)
		return Endpoint{}, err
	}
	if _, err := d.observationRecord(id); err != nil {
		return Endpoint{}, err
	}
	record, err := d.actualRecord(id)
	if err != nil {
		return Endpoint{}, err
	}
	insp, err := d.verifyContainer(ctx, record)
	if err != nil {
		d.revoke(id)
		return Endpoint{}, err
	}
	live, _ := insp["State"].(map[string]any)
	if live["Running"] != true {
		d.revoke(id)
		return Endpoint{}, fmt.Errorf("docker: actual container is not running")
	}
	if _, err := d.observationRecord(id); err != nil {
		return Endpoint{}, err
	}
	if err := ctx.Err(); err != nil {
		d.revoke(id)
		return Endpoint{}, err
	}
	d.mu.Lock()
	u := d.endpoints[id]
	d.mu.Unlock()
	if err := ctx.Err(); err != nil {
		d.revoke(id)
		return Endpoint{}, err
	}
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
		if err := ctx.Err(); err != nil {
			return nil, err
		}
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
			_, endpointErr := d.endpoint(ctx, id)
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			st.Ready = st.Running && endpointErr == nil
		}
		out = append(out, st)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// instancePort reads the single TCP binding set by Ensure. Image EXPOSE values
// may be merged into Config.ExposedPorts, so they cannot identify our listener.
// Desired specs may already describe a pending replacement and are not evidence
// of the still-running container's port either.
func instancePort(insp map[string]any) (uint32, error) {
	host, _ := insp["HostConfig"].(map[string]any)
	bindings, _ := host["PortBindings"].(map[string]any)
	var port uint32
	for key, raw := range bindings {
		number, protocol, ok := strings.Cut(key, "/")
		parsed, err := strconv.ParseUint(number, 10, 16)
		if !ok || err != nil || parsed == 0 || strconv.FormatUint(parsed, 10) != number ||
			(protocol != "tcp" && protocol != "udp" && protocol != "sctp") {
			return 0, fmt.Errorf("invalid container port binding")
		}
		if protocol != "tcp" {
			continue
		}
		values, ok := raw.([]any)
		if !ok || len(values) != 1 {
			return 0, fmt.Errorf("invalid container TCP binding")
		}
		if _, ok := values[0].(map[string]any); !ok {
			return 0, fmt.Errorf("invalid container TCP binding")
		}
		if port != 0 {
			return 0, fmt.Errorf("ambiguous container TCP bindings")
		}
		port = uint32(parsed)
	}
	if port == 0 {
		return 0, fmt.Errorf("missing container TCP binding")
	}
	return port, nil
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
