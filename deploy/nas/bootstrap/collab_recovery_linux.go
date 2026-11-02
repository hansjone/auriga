package main

import (
	"fmt"
	"path/filepath"
	"strings"
)

func (r *nasRotation) messageRecoveryCommand() string {
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'" }
	return "AURIGA_DATA_ROOT=" + quote(r.data) + " sh " + quote(filepath.Join(r.recipe, "scripts/start-collab-network.sh"))
}

// A read-only availability observation, never authority to recreate services.
func (e *nasRotationEngine) messageAvailability(r *nasRotation) error {
	all, err := e.containers()
	if err != nil {
		return err
	}
	ready := map[string]bool{"collab": false, "network": false}
	seen := map[string]bool{}
	for _, c := range all {
		if c.Config.Labels["com.docker.compose.project"] != nasRotationProject {
			continue
		}
		role := c.Config.Labels["com.docker.compose.service"]
		if _, wanted := ready[role]; !wanted {
			continue
		}
		if seen[role] {
			return fmt.Errorf("duplicate message consumer")
		}
		seen[role] = true
		if err = e.owned(r, c); err != nil {
			return err
		}
		ready[role] = c.State.Running && !c.State.Paused && !c.State.Restarting && (c.State.Health.Status == "healthy" || c.State.Health.Status == "")
	}
	if !ready["collab"] || !ready["network"] {
		return fmt.Errorf("message consumers missing, stopped or unhealthy")
	}
	return nil
}
