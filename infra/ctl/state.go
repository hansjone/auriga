package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"
)

const (
	lockFileName  = "dev.lock"
	stateFileName = "ctl.state.json"
	stopFileName  = "ctl.stop"
	logFileName   = "ctl.log"
)

// ctlState is persisted by the background serve process (no secrets).
type ctlState struct {
	PID       int       `json:"pid"`
	PGPort    uint32    `json:"pgPort"`
	NATSPort  int       `json:"natsPort"`
	DataDir   string    `json:"dataDir"`
	Ready     bool      `json:"ready"`
	StartedAt time.Time `json:"startedAt"`
}

func lockPath(dataDir string) string  { return filepath.Join(dataDir, lockFileName) }
func statePath(dataDir string) string { return filepath.Join(dataDir, stateFileName) }
func stopPath(dataDir string) string  { return filepath.Join(dataDir, stopFileName) }
func logPath(dataDir string) string   { return filepath.Join(dataDir, logFileName) }

func writeState(dataDir string, state ctlState) error {
	body, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	tmp := statePath(dataDir) + ".tmp"
	if err := os.WriteFile(tmp, append(body, '\n'), 0600); err != nil {
		return err
	}
	return os.Rename(tmp, statePath(dataDir))
}

func readState(dataDir string) (ctlState, error) {
	var state ctlState
	body, err := os.ReadFile(statePath(dataDir))
	if err != nil {
		return state, err
	}
	if err := json.Unmarshal(body, &state); err != nil {
		return state, fmt.Errorf("invalid %s: %w", stateFileName, err)
	}
	return state, nil
}

func removeStateFiles(dataDir string) error {
	return errors.Join(
		os.Remove(statePath(dataDir)),
		os.Remove(stopPath(dataDir)),
	)
}

func portOpen(host string, port int) bool {
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(host, fmt.Sprintf("%d", port)), 200*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// runningFromState reports whether a prior start/serve instance looks alive.
func runningFromState(dataDir string) (ctlState, bool, error) {
	state, err := readState(dataDir)
	if errors.Is(err, os.ErrNotExist) {
		return state, false, nil
	}
	if err != nil {
		return state, false, err
	}
	if state.PID <= 0 || !processAlive(state.PID) {
		return state, false, nil
	}
	if !state.Ready {
		return state, true, nil
	}
	if !portOpen("127.0.0.1", int(state.PGPort)) || !portOpen("127.0.0.1", state.NATSPort) {
		// Process still listed but listeners gone — treat as not running for start/stop.
		return state, false, nil
	}
	return state, true, nil
}

func readLockPID(dataDir string) (int, error) {
	body, err := os.ReadFile(lockPath(dataDir))
	if err != nil {
		return 0, err
	}
	var pid int
	if _, err := fmt.Sscanf(string(body), "%d", &pid); err != nil || pid <= 0 {
		return 0, fmt.Errorf("invalid %s", lockFileName)
	}
	return pid, nil
}

func clearStaleRuntime(dataDir string) error {
	state, running, err := runningFromState(dataDir)
	if err != nil {
		return err
	}
	if running {
		return fmt.Errorf("services already running (pid %d)", state.PID)
	}
	if pid, lockErr := readLockPID(dataDir); lockErr == nil {
		if processAlive(pid) {
			return fmt.Errorf("data directory locked by pid %d", pid)
		}
	} else if !errors.Is(lockErr, os.ErrNotExist) {
		if state.PGPort > 0 && (portOpen("127.0.0.1", int(state.PGPort)) || portOpen("127.0.0.1", state.NATSPort)) {
			return fmt.Errorf("refusing to clear %s while listeners are active", lockFileName)
		}
	}
	_ = os.Remove(stopPath(dataDir))
	_ = os.Remove(statePath(dataDir))
	if _, err := os.Stat(lockPath(dataDir)); err == nil {
		if err := os.Remove(lockPath(dataDir)); err != nil {
			return fmt.Errorf("remove stale %s: %w", lockFileName, err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
