package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/hansjone/auriga/infra/agent/internal/state"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
)

type engineNetwork struct {
	ID         string `json:"Id"`
	Name       string
	Driver     string
	Internal   bool
	Labels     map[string]string
	Containers map[string]struct{ Name string }
	bridges    []trustedBridge
}

const egressHost = "auriga-egress.internal"

type trustedBridge struct {
	id      string
	aliases []string
	process processLifetime
}

func (n *engineNetwork) bridgesReady() bool {
	for _, bridge := range n.bridges {
		if _, exists := n.Containers[bridge.id]; !exists {
			return false
		}
	}
	return true
}

func sameBridges(a, b []trustedBridge) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].id != b[i].id || a[i].process != b[i].process {
			return false
		}
	}
	return true
}

func (c *EngineClient) NetworkInspect(ctx context.Context, name string) (*engineNetwork, error) {
	raw, status, err := c.doJSON(ctx, http.MethodGet, "/networks/"+url.PathEscape(name), nil, http.StatusOK, http.StatusNotFound)
	if err != nil {
		return nil, err
	}
	if status == http.StatusNotFound {
		return nil, nil
	}
	var out engineNetwork
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

var edgeContainerRef = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,127}$`)
var dnsLabel = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)

func validateEdgeNetworkConfig(container, host string) error {
	if container == "" && host == "" {
		return nil
	}
	if !edgeContainerRef.MatchString(container) || len(host) > 253 || net.ParseIP(host) != nil {
		return fmt.Errorf("docker: edge container and platform DNS hostname must both be configured")
	}
	for _, label := range strings.Split(host, ".") {
		if !dnsLabel.MatchString(label) {
			return fmt.Errorf("docker: invalid platform DNS hostname")
		}
	}
	return nil
}

func (d *Docker) ownedNetwork(ctx context.Context, id string) (*engineNetwork, error) {
	n, err := d.Client.NetworkInspect(ctx, networkName(id))
	if err != nil || n == nil {
		return n, err
	}
	if n.ID == "" || n.Name != networkName(id) || n.Driver != "bridge" || !n.Internal ||
		n.Labels[labelManaged] != "true" || n.Labels[labelInstance] != id {
		return nil, fmt.Errorf("docker: instance network ownership/isolation mismatch")
	}
	return n, nil
}

func (d *Docker) trustedEdge(ctx context.Context, requireRunning bool) (string, map[string]any, error) {
	insp, err := d.Client.ContainerInspect(ctx, d.EdgeContainer)
	if err != nil {
		return "", nil, err
	}
	cfg, _ := insp["Config"].(map[string]any)
	labels, _ := cfg["Labels"].(map[string]any)
	state, _ := insp["State"].(map[string]any)
	host, _ := insp["HostConfig"].(map[string]any)
	id, _ := insp["Id"].(string)
	mode, _ := host["NetworkMode"].(string)
	if id == "" || labels["auriga.role"] != "edge" || (requireRunning && state["Running"] != true) || mode == "" || mode == "host" || mode == "none" || strings.HasPrefix(mode, "container:") {
		return "", nil, fmt.Errorf("docker: configured edge must be a running labelled bridge container")
	}
	return id, insp, nil
}

func edgeHasAlias(insp map[string]any, n *engineNetwork, alias string) bool {
	settings, _ := insp["NetworkSettings"].(map[string]any)
	nets, _ := settings["Networks"].(map[string]any)
	ep, _ := nets[n.Name].(map[string]any)
	if ep["NetworkID"] != n.ID {
		return false
	}
	aliases, _ := ep["Aliases"].([]any)
	for _, a := range aliases {
		if a == alias {
			return true
		}
	}
	return false
}

func (d *Docker) edgeAliases() []string {
	aliases := []string{d.PlatformHost}
	for _, host := range append([]string{d.PortalHost}, d.ManagedHosts...) {
		if host != "" && !slices.Contains(aliases, host) {
			aliases = append(aliases, host)
		}
	}
	return aliases
}

func (d *Docker) edgeHasAliases(insp map[string]any, n *engineNetwork) bool {
	for _, alias := range d.edgeAliases() {
		if !edgeHasAlias(insp, n, alias) {
			return false
		}
	}
	return true
}

func (d *Docker) ensureEdgeNetwork(ctx context.Context, id string) error {
	var record *state.ExecutionRecord
	if scope, ok := ctx.Value(activationKey{}).(*activationScope); ok {
		record = scope.record
	}
	n, _, _, err := d.inspectEdgeNetwork(ctx, id, record)
	if err != nil {
		return err
	}
	if n.bridgesReady() {
		return nil
	}
	for _, bridge := range n.bridges {
		if _, exists := n.Containers[bridge.id]; exists {
			continue
		}
		_, _, err = d.Client.doJSON(ctx, http.MethodPost, "/networks/"+url.PathEscape(n.ID)+"/connect", map[string]any{
			"Container": bridge.id, "EndpointConfig": map[string]any{"Aliases": bridge.aliases},
		}, http.StatusOK)
		if err != nil {
			return err
		}
	}
	// Inspect again: a successful write is not sufficient evidence of the alias.
	after, _, _, err := d.inspectEdgeNetwork(ctx, id, record)
	if err != nil {
		return fmt.Errorf("docker: trusted bridge alias was not applied: %w", err)
	}
	if after.ID != n.ID || !after.bridgesReady() || !sameBridges(after.bridges, n.bridges) {
		return fmt.Errorf("docker: trusted bridge alias was not applied")
	}
	return nil
}

// The same ownership/isolation checks protect both read-only maintenance and
// coordinated repair. A missing trusted edge is repairable; contamination is not.
func (d *Docker) inspectEdgeNetwork(ctx context.Context, id string, record *state.ExecutionRecord) (*engineNetwork, string, map[string]any, error) {
	// Only the matching managed instance and the trusted edge may share this
	// network. Never attach the edge to a name-collision or contaminated network.
	var instance map[string]any
	var err error
	if record != nil {
		instance, err = d.verifyContainer(ctx, record)
	} else {
		instance, err = d.Client.ContainerInspect(ctx, containerName(id))
	}
	if err != nil {
		return nil, "", nil, err
	}
	instanceID, _ := instance["Id"].(string)
	if instance != nil {
		cfg, _ := instance["Config"].(map[string]any)
		labels, _ := cfg["Labels"].(map[string]any)
		if instanceID == "" || labels[labelManaged] != "true" || labels[labelInstance] != id {
			return nil, "", nil, fmt.Errorf("docker: existing instance container ownership mismatch")
		}
	}
	// Read topology after the container snapshot, so a final container inspect
	// cannot hide an edge endpoint lost immediately before that inspect replied.
	n, err := d.ownedNetwork(ctx, id)
	if err != nil {
		return nil, "", nil, err
	}
	if n == nil {
		return nil, "", nil, fmt.Errorf("docker: instance network disappeared")
	}
	edgeID, insp, err := d.trustedEdge(ctx, true)
	if err != nil {
		return nil, "", nil, err
	}
	n.bridges = []trustedBridge{{id: edgeID, aliases: d.edgeAliases()}}
	if d.EgressContainer != "" {
		egress, egressInspect, err := d.trustedEgress(ctx, insp, true)
		if err != nil {
			return nil, "", nil, err
		}
		process, err := containerProcess(egressInspect)
		if err != nil {
			return nil, "", nil, err
		}
		n.bridges = append(n.bridges, trustedBridge{id: egress, aliases: []string{egressHost}, process: process})
		if _, exists := n.Containers[egress]; exists && !edgeHasAlias(egressInspect, n, egressHost) {
			return nil, "", nil, fmt.Errorf("docker: existing egress endpoint has wrong alias")
		}
	}
	for member := range n.Containers {
		trusted := member == instanceID
		for _, bridge := range n.bridges {
			trusted = trusted || member == bridge.id
		}
		if !trusted {
			return nil, "", nil, fmt.Errorf("docker: unexpected instance network member")
		}
	}
	if _, exists := n.Containers[edgeID]; exists {
		if !d.edgeHasAliases(insp, n) {
			return nil, "", nil, fmt.Errorf("docker: existing edge endpoint has wrong platform alias")
		}
	}
	return n, edgeID, instance, nil
}

func (d *Docker) detachEdge(ctx context.Context, n *engineNetwork) error {
	edgeID, edge, err := d.trustedEdge(ctx, false)
	if err != nil {
		return err
	}
	bridges := []string{edgeID}
	if d.EgressContainer != "" {
		egressID, _, err := d.trustedEgress(ctx, edge, false)
		if err != nil {
			return err
		}
		bridges = append(bridges, egressID)
	}
	for _, id := range bridges {
		if _, exists := n.Containers[id]; !exists {
			continue
		}
		_, _, err = d.Client.doJSON(ctx, http.MethodPost, "/networks/"+url.PathEscape(n.ID)+"/disconnect",
			map[string]any{"Container": id, "Force": false}, http.StatusOK)
		if err != nil {
			return err
		}
	}
	return nil
}

// Egress is a separate least-privilege process in the exact trusted edge image.
// A role label alone cannot replace its executable, project or process identity.
func (d *Docker) trustedEgress(ctx context.Context, edge map[string]any, requireRunning bool) (string, map[string]any, error) {
	insp, err := d.Client.ContainerInspect(ctx, d.EgressContainer)
	if err != nil {
		return "", nil, err
	}
	cfg, _ := insp["Config"].(map[string]any)
	labels, _ := cfg["Labels"].(map[string]any)
	edgeCfg, _ := edge["Config"].(map[string]any)
	edgeLabels, _ := edgeCfg["Labels"].(map[string]any)
	state, _ := insp["State"].(map[string]any)
	health, _ := state["Health"].(map[string]any)
	host, _ := insp["HostConfig"].(map[string]any)
	id, _ := insp["Id"].(string)
	image, _ := insp["Image"].(string)
	edgeImage, _ := edge["Image"].(string)
	project, _ := edgeLabels["com.docker.compose.project"].(string)
	mode, _ := host["NetworkMode"].(string)
	mounts, _ := insp["Mounts"].([]any)
	ports, _ := host["PortBindings"].(map[string]any)
	capAdd, _ := host["CapAdd"].([]any)
	if id == "" || image == "" || image != edgeImage || project == "" ||
		labels["auriga.role"] != "egress" || labels["auriga.egress.for-edge"] != d.EdgeContainer || labels["com.docker.compose.project"] != project ||
		cfg["User"] != "65532:65532" || host["ReadonlyRootfs"] != true || host["Privileged"] != false ||
		!sameStrings(cfg["Entrypoint"], []string{"/auriga-egress"}) || !sameStrings(cfg["Cmd"], []string{"--listen", "0.0.0.0:3128"}) ||
		!trustedEgressMounts(mounts, project) || len(ports) != 0 || len(capAdd) != 0 ||
		mode == "" || mode == "host" || mode == "none" || strings.HasPrefix(mode, "container:") ||
		!containsString(host["CapDrop"], "ALL") || !containsString(host["SecurityOpt"], "no-new-privileges:true") ||
		(requireRunning && (state["Running"] != true || health["Status"] != "healthy")) {
		return "", nil, fmt.Errorf("docker: configured egress identity or isolation mismatch")
	}
	return id, insp, nil
}

func trustedEgressMounts(mounts []any, project string) bool {
	// Older deployments have no platform policy volumes. New deployments may
	// expose only the same project's read-only policy and writable report volumes.
	if len(mounts) == 0 {
		return true
	}
	if len(mounts) != 2 {
		return false
	}
	seen := make(map[string]bool, 2)
	for _, value := range mounts {
		mount, _ := value.(map[string]any)
		name, _ := mount["Name"].(string)
		writable := false
		switch name {
		case project + "_egress-policies":
			if mount["Destination"] != "/var/auriga-egress/policies" {
				return false
			}
		case project + "_egress-reports":
			if mount["Destination"] != "/var/auriga-egress/reports" {
				return false
			}
			writable = true
		default:
			return false
		}
		if mount["Type"] != "volume" || mount["RW"] != writable || seen[name] {
			return false
		}
		seen[name] = true
	}
	return true
}

func containsString(value any, want string) bool {
	items, _ := value.([]any)
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}

func sameStrings(value any, want []string) bool {
	items, _ := value.([]any)
	if len(items) != len(want) {
		return false
	}
	for i, item := range items {
		if item != want[i] {
			return false
		}
	}
	return true
}
