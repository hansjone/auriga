package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
)

func setGateProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
	cmd.WaitDelay = 5 * time.Second
}
func stopGateProcessGroup(cmd *exec.Cmd, force bool) {
	signal := syscall.SIGTERM
	if force {
		signal = syscall.SIGKILL
	}
	_ = syscall.Kill(-cmd.Process.Pid, signal)
}

// Called only after the owned daemon and containers have stopped. All mount
// points are constrained to this gate's private data/runtime directories.
func unmountGatePaths(roots ...string) error {
	raw, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return err
	}
	return unmountGateMountInfo(raw, roots...)
}

func unmountGateMountInfo(raw []byte, roots ...string) error {
	decode := strings.NewReplacer(`\040`, " ", `\011`, "\t", `\012`, "\n", `\134`, `\`)
	var paths []string
	for _, line := range strings.Split(string(raw), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 5 {
			continue
		}
		path := decode.Replace(fields[4])
		for _, root := range roots {
			rel, err := filepath.Rel(root, path)
			if err == nil && (rel == "." || filepath.IsLocal(rel)) {
				paths = append(paths, path)
				break
			}
		}
	}
	sort.Slice(paths, func(i, j int) bool { return len(paths[i]) > len(paths[j]) })
	var result error
	for _, path := range paths {
		if err := syscall.Unmount(path, 0); err != nil && !errors.Is(err, syscall.EINVAL) && !errors.Is(err, syscall.ENOENT) {
			result = errors.Join(result, &os.PathError{Op: "unmount gate", Path: path, Err: err})
		}
	}
	return result
}
