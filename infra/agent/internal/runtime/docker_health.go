package runtime

import (
	"context"
	"fmt"
	"net/url"
	"time"

	"github.com/hansjone/auriga/infra/agent/internal/observer"
)

// CheckHealth does not revoke a healthy bridge or perform Engine writes. Repair
// remains inside ReleaseActivation.Recover and Start, with the original release.
func (d *Docker) CheckHealth(ctx context.Context, id string) (st Status, err error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	st.InstanceID = id
	defer func() {
		if err != nil || !st.Ready {
			d.revoke(id)
		}
	}()
	record, err := d.observationRecord(id)
	if err != nil {
		return st, err
	}
	st.Generation = record.Generation
	insp, err := d.verifyContainer(ctx, record)
	if err != nil {
		return st, err
	}
	live, _ := insp["State"].(map[string]any)
	st.Running = live["Running"] == true
	if !st.Running {
		return st, nil
	}
	process, err := containerProcess(insp)
	if err != nil {
		return st, err
	}
	if d.EdgeContainer != "" {
		n, edgeID, _, networkErr := d.inspectEdgeNetwork(ctx, id, record)
		if networkErr != nil {
			return st, networkErr
		}
		if _, connected := n.Containers[edgeID]; !connected {
			return st, nil
		}
	}
	port, err := instancePort(insp)
	if err != nil {
		return st, err
	}
	endpoint, err := instanceEndpoint(insp, networkName(id), port)
	if err != nil {
		return st, err
	}
	base, err := url.Parse(endpoint)
	if err != nil {
		return st, err
	}
	result := observer.ProbeHealth(ctx, nil, observer.InstanceTarget{ID: id, BaseURL: base, Process: observer.ProcessRunning})
	if !result.Ready {
		return st, nil
	}
	// A late positive response cannot qualify a replaced, revoked or stopped
	// execution. It also cannot republish an endpoint cleared by another action.
	current, err := d.observationRecord(id)
	if err != nil {
		return st, err
	}
	if current.ContainerID != record.ContainerID || current.Generation != record.Generation {
		return st, fmt.Errorf("docker: execution changed during health check")
	}
	if d.EdgeContainer != "" {
		n, edgeID, _, networkErr := d.inspectEdgeNetwork(ctx, id, record)
		if networkErr != nil {
			return st, networkErr
		}
		if _, connected := n.Containers[edgeID]; !connected {
			return st, nil
		}
	}
	after, err := d.verifyContainer(ctx, record)
	if err != nil {
		return st, err
	}
	state, _ := after["State"].(map[string]any)
	if state["Running"] != true {
		st.Running = false
		return st, nil
	}
	finalProcess, err := containerProcess(after)
	if err != nil {
		return st, err
	}
	if process != finalProcess {
		return st, fmt.Errorf("docker: process changed during health check")
	}
	if err := ctx.Err(); err != nil {
		return st, err
	}
	d.mu.Lock()
	ep := d.endpoints[id]
	st.Ready = ep.url == endpoint && ep.process == process
	d.mu.Unlock()
	return st, nil
}
