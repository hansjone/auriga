package main

import (
	"fmt"
	"path/filepath"
)

// Recovery may reapply the edge service. Refuse an existing foreign consumer
// before Compose can replace it. A missing service may be created normally.
func (e *nasRotationEngine) checkRecoveryEdge(r *nasRotation) error {
	all, err := e.containers()
	if err != nil {
		return err
	}
	found := false
	for _, c := range all {
		labels := c.Config.Labels
		if c.Name != "/auriga-nas-test-edge" && !(labels["com.docker.compose.project"] == nasRotationProject && labels["com.docker.compose.service"] == "edge") {
			continue
		}
		if found || labels["com.docker.compose.service"] != "edge" {
			return fmt.Errorf("NAS recovery duplicate or foreign edge refused")
		}
		found = true
		if err = e.owned(r, c); err != nil {
			return err
		}
		config := false
		for _, m := range c.Mounts {
			if m.Destination == "/etc/auriga/edge.config.json" {
				if config || m.RW {
					return fmt.Errorf("NAS recovery edge config mount refused")
				}
				switch m.Source {
				case filepath.Join(r.data, "config/materials/edge.config.json"), filepath.Join(r.data, "config/collab-runtime/edge/edge.config.json"):
				case filepath.Join(r.data, "config/bifrost/edge.config.json"):
					if err := checkNASBifrostEdge(filepath.Join(r.data, "config"), true); err != nil {
						return fmt.Errorf("NAS recovery Bifrost edge projection refused: %w", err)
					}
				default:
					return fmt.Errorf("NAS recovery edge config mount refused")
				}
				config = true
			}
		}
		if !config {
			return fmt.Errorf("NAS recovery edge config missing")
		}
	}
	return nil
}
