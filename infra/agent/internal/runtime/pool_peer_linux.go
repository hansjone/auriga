//go:build linux

package runtime

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type procIdentity struct {
	parent int
	start  string
}

func readPoolProc(pid int) (procIdentity, error) {
	raw, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return procIdentity{}, err
	}
	end := strings.LastIndex(string(raw), ")")
	if end < 0 {
		return procIdentity{}, fmt.Errorf("invalid kernel process identity")
	}
	fields := strings.Fields(string(raw)[end+1:])
	if len(fields) < 20 {
		return procIdentity{}, fmt.Errorf("short kernel process identity")
	}
	parent, err := strconv.Atoi(fields[1])
	if err != nil {
		return procIdentity{}, err
	}
	if _, err = strconv.ParseUint(fields[19], 10, 64); err != nil {
		return procIdentity{}, err
	}
	return procIdentity{parent: parent, start: fields[19]}, nil
}

// VerifyPoolPeer joins a kernel socket peer to the current signed container,
// exact process lifetime and immutable mounts. Agent runs in the host PID
// namespace, while the Host retains its private PID namespace and no privileges.
func (d *Docker) VerifyPoolPeer(ctx context.Context, pool string, pid int, start string) (string, error) {
	if d.Activation == nil || pid <= 0 {
		return "", fmt.Errorf("pool execution unavailable")
	}
	record, err := d.Activation.Store.GetExecution(pool)
	if err != nil || record == nil {
		return "", fmt.Errorf("pool execution unavailable")
	}
	actual, err := d.verifyContainer(ctx, record)
	if err != nil {
		return "", err
	}
	process, err := containerProcess(actual)
	if err != nil {
		return "", err
	}
	if record.Phase == "started" {
		var saved poolProcess
		file := filepath.Join(d.DataRoot, "pools", pool, "control", "process-state")
		if err := unifiedRead(file, &saved); err != nil || saved.Container != record.ContainerID || saved.PID != process.pid || !saved.StartedAt.Equal(process.startedAt) {
			return "", fmt.Errorf("pool process lifetime changed")
		}
	}
	peer, err := readPoolProc(pid)
	if err != nil || peer.start != start {
		return "", fmt.Errorf("socket peer process changed")
	}
	root, err := readPoolProc(int(process.pid))
	if err != nil {
		return "", err
	}
	current := pid
	for depth := 0; current != int(process.pid); depth++ {
		if depth >= 128 || current <= 1 {
			return "", fmt.Errorf("socket peer is outside signed pool process")
		}
		identity, err := readPoolProc(current)
		if err != nil || identity.parent == current {
			return "", fmt.Errorf("invalid process ancestry")
		}
		current = identity.parent
	}
	// Recheck the endpoints after the ancestry walk to close process-ID reuse.
	peerAfter, e1 := readPoolProc(pid)
	rootAfter, e2 := readPoolProc(int(process.pid))
	if e1 != nil || e2 != nil || peerAfter != peer || rootAfter != root {
		return "", fmt.Errorf("pool process changed during proof")
	}
	return fmt.Sprintf("%s/%d/%s/%s/%d/%s", record.ContainerID, process.pid, root.start, process.startedAt.UTC().Format("2006-01-02T15:04:05.999999999Z"), pid, peer.start), nil
}
