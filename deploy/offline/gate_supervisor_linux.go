package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"
)

func awaitGateSupervisor(ctx context.Context, root string) error {
	if os.Getpid() != 1 {
		return errors.New("受监督门禁必须为隔离PID 1")
	}
	if err := os.WriteFile(filepath.Join(root, "supervisor-ready"), nil, 0600); err != nil {
		return err
	}
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		if _, err := os.Stat(filepath.Join(root, "supervisor-ack")); err == nil {
			return ctx.Err()
		} else if !os.IsNotExist(err) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C:
		}
	}
}

// CLONE_NEWPID makes unshare, and then the exec'd gate, our direct child and
// namespace init. wait4 cannot finish until Linux has killed and reaped that
// init's namespace descendants. This also covers the WaitDelay SIGKILL path.
func runGateProcess(ctx context.Context, cmd *exec.Cmd, workspace *gateWorkspace, exe string) error {
	setGateProcessGroup(cmd)
	cmd.SysProcAttr.Cloneflags = syscall.CLONE_NEWPID
	cmd.WaitDelay = 45 * time.Second
	if err := cmd.Start(); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	var mountNS *os.File
	defer func() {
		if mountNS != nil {
			_ = mountNS.Close()
		}
	}()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	cancelled := ctx.Done()
	var deadline <-chan time.Time
	var startupErr error
	var waitErr error
	waiting := true
	for waiting {
		select {
		case waitErr = <-done:
			waiting = false
		case <-cancelled:
			cancelled = nil
			deadline = time.After(60 * time.Second)
		case <-deadline:
			_ = cmd.Process.Kill()
			return retainGateWorkspace(workspace, fmt.Errorf("隔离PID 1退出未确认（PID %d）: %w", cmd.Process.Pid, errors.Join(startupErr, ctx.Err())))
		case <-tick.C:
			if mountNS != nil || startupErr != nil {
				continue
			}
			if _, err := os.Stat(filepath.Join(workspace.root, "supervisor-ready")); os.IsNotExist(err) {
				continue
			} else if err != nil {
				startupErr = err
			} else {
				mountNS, startupErr = os.Open(fmt.Sprintf("/proc/%d/ns/mnt", cmd.Process.Pid))
				if startupErr == nil {
					startupErr = os.WriteFile(filepath.Join(workspace.root, "supervisor-ack"), nil, 0600)
				}
			}
			if startupErr != nil {
				_ = cmd.Process.Kill()
				deadline = time.After(15 * time.Second)
			}
		}
	}
	if mountNS != nil {
		if err := reclaimGateMounts(mountNS, workspace, exe); err != nil {
			return retainGateWorkspace(workspace, errors.Join(startupErr, waitErr, ctx.Err(), err))
		}
	}
	return errors.Join(startupErr, waitErr, ctx.Err())
}

func retainGateWorkspace(workspace *gateWorkspace, cause error) error {
	workspace.retained = true
	log, _ := smallFile(filepath.Join(workspace.root, "daemon.log"), 4<<20)
	return fmt.Errorf("门禁资源回收未确认，保留工作目录 %s 与运行目录 %s: %w\n%s", workspace.root, workspace.runtime, cause, log)
}

// Hold the exact mount namespace before allowing the child to start dockerd.
// After its init has been waited, a separate helper unmounts its owned paths.
func reclaimGateMounts(mountNS *os.File, workspace *gateWorkspace, exe string) error {
	proc, err := os.Open("/proc")
	if err != nil {
		return err
	}
	defer proc.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "nsenter", "--mount=/proc/self/fd/3", "--", exe, "internal-offline-cleanup", "--workdir", workspace.root, "--runtime-dir", workspace.runtime)
	cmd.ExtraFiles = []*os.File{mountNS, proc}
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	setGateProcessGroup(cmd)
	if err := cmd.Start(); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		return err
	case <-time.After(20 * time.Second):
		_ = cmd.Process.Kill()
		return fmt.Errorf("门禁挂载清理进程退出未确认（PID %d）", cmd.Process.Pid)
	}
}

func gateNamespaceCleanup(args []string) error {
	f := flags("internal-offline-cleanup")
	root := f.String("workdir", "", "")
	runtime := f.String("runtime-dir", "", "")
	if err := f.Parse(args); err != nil {
		return err
	}
	if *root == "" || *runtime == "" || !filepath.IsAbs(*root) || !filepath.IsAbs(*runtime) || f.NArg() != 0 {
		return errors.New("门禁挂载清理需要完整的私有目录")
	}
	// /proc in this mount namespace belongs to its now-dead PID namespace.
	// Resolve self through the inherited host proc directory instead.
	fd, err := syscall.Openat(4, "self/mountinfo", syscall.O_RDONLY|syscall.O_CLOEXEC, 0)
	if err != nil {
		return err
	}
	file := os.NewFile(uintptr(fd), "gate-mountinfo")
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, (16<<20)+1))
	if err != nil {
		return err
	}
	if len(raw) > 16<<20 {
		return errors.New("门禁挂载清单超出安全读取上限，无法确认清理范围")
	}
	return unmountGateMountInfo(raw, *root, *runtime)
}
