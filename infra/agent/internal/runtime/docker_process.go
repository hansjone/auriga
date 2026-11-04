package runtime

import (
	"fmt"
	"math"
	"time"
)

// Container identity survives process restarts. A readiness proof belongs to one
// actual process lifetime, identified by both the Engine start time and host PID.
type processLifetime struct {
	startedAt time.Time
	pid       int64
}

type dockerEndpoint struct {
	url     string
	process processLifetime
}

func containerProcess(insp map[string]any) (processLifetime, error) {
	state, _ := insp["State"].(map[string]any)
	startedAt, startOK := state["StartedAt"].(string)
	pid, pidOK := state["Pid"].(float64)
	started, err := time.Parse(time.RFC3339Nano, startedAt)
	if state["Running"] != true || !startOK || err != nil || started.IsZero() || !pidOK || pid <= 0 || pid > 1<<53-1 || math.Trunc(pid) != pid {
		return processLifetime{}, fmt.Errorf("docker: running process lifetime is unavailable")
	}
	return processLifetime{startedAt: started.UTC(), pid: int64(pid)}, nil
}
