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
	n, edgeID, _, err := d.inspectEdgeNetwork(ctx, id, record)
	if err != nil {
		return err
	}
	if _, exists := n.Containers[edgeID]; exists {
		return nil
	}
	_, _, err = d.Client.doJSON(ctx, http.MethodPost, "/networks/"+url.PathEscape(n.ID)+"/connect", map[string]any{
		"Container": edgeID, "EndpointConfig": map[string]any{"Aliases": d.edgeAliases()},
	}, http.StatusOK)
	if err != nil {
		return err
	}
	// Inspect again: a successful write is not sufficient evidence of the alias.
	edgeIDAfter, after, err := d.trustedEdge(ctx, true)
	if err != nil {
		return err
	}
	if edgeIDAfter != edgeID || !d.edgeHasAliases(after, n) {
		return fmt.Errorf("docker: edge platform alias was not applied")
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
	for member := range n.Containers {
		if member != edgeID && member != instanceID {
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
	edgeID, _, err := d.trustedEdge(ctx, false)
	if err != nil {
		return err
	}
	if _, exists := n.Containers[edgeID]; !exists {
		return nil
	}
	_, _, err = d.Client.doJSON(ctx, http.MethodPost, "/networks/"+url.PathEscape(n.ID)+"/disconnect",
		map[string]any{"Container": edgeID, "Force": false}, http.StatusOK)
	return err
}
