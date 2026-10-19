package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

const (
	statusExitRunning = 0
	statusExitStopped = 3
	serveReadyWait    = 90 * time.Second
	stopGracePeriod   = 60 * time.Second
)

func runServe(ctx context.Context, options devOptions, out, logs io.Writer) (result error) {
	logger := slog.New(slog.NewJSONHandler(logs, nil)).With("service", "ctl", "mode", "serve")
	o, err := options.normalized()
	if err != nil {
		return err
	}
	_ = os.Remove(stopPath(o.DataDir))
	state := ctlState{
		PID:       os.Getpid(),
		PGPort:    o.PGPort,
		NATSPort:  o.NATSPort,
		DataDir:   o.DataDir,
		Ready:     false,
		StartedAt: time.Now().UTC(),
	}
	if err := writeState(o.DataDir, state); err != nil {
		return err
	}
	defer func() {
		_ = os.Remove(stopPath(o.DataDir))
		_ = os.Remove(statePath(o.DataDir))
	}()

	services, err := startDev(ctx, o, logger)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return nil
		}
		return err
	}
	defer func() { result = errors.Join(result, services.Close()) }()

	state.Ready = true
	if err := writeState(o.DataDir, state); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(out, "ready pg=%d nats=%d\n", o.PGPort, o.NATSPort); err != nil {
		return err
	}
	logger.Info("background services ready", "pgPort", o.PGPort, "natsPort", o.NATSPort)

	stopTicker := time.NewTicker(200 * time.Millisecond)
	defer stopTicker.Stop()
	for {
		select {
		case <-ctx.Done():
			logger.Info("stopping background services", "reason", "signal")
			return nil
		case <-stopTicker.C:
			if _, err := os.Stat(stopPath(o.DataDir)); err == nil {
				logger.Info("stopping background services", "reason", "stop-file")
				return nil
			} else if !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
	}
}

func runStart(ctx context.Context, options devOptions, out, logs io.Writer) error {
	o, err := options.normalized()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(o.DataDir, 0700); err != nil {
		return err
	}
	if state, running, err := runningFromState(o.DataDir); err != nil {
		return err
	} else if running {
		_, err := fmt.Fprintf(out, "already running pid=%d data-dir=%s pg=%d nats=%d\n",
			state.PID, o.DataDir, state.PGPort, state.NATSPort)
		return err
	}
	if err := clearStaleRuntime(o.DataDir); err != nil {
		return err
	}

	exe, err := os.Executable()
	if err != nil {
		return err
	}
	args := []string{
		"serve",
		"--data-dir", o.DataDir,
		"--cache-dir", o.CacheDir,
		"--runtime-dir", o.RuntimeDir,
		"--pg-port", fmt.Sprintf("%d", o.PGPort),
		"--nats-port", fmt.Sprintf("%d", o.NATSPort),
	}
	cmd := exec.Command(exe, args...)
	logFile, err := os.OpenFile(logPath(o.DataDir), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer func() { _ = logFile.Close() }()
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	configureDaemonCmd(cmd)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("spawn serve: %w", err)
	}
	// Parent must not wait on the detached child for the whole lifetime.
	go func() { _ = cmd.Wait() }()

	deadline := time.Now().Add(serveReadyWait)
	for time.Now().Before(deadline) {
		if err := ctx.Err(); err != nil {
			_ = requestStop(o.DataDir, cmd.Process.Pid)
			return err
		}
		state, err := readState(o.DataDir)
		if err == nil && state.Ready && state.PID == cmd.Process.Pid {
			_, err := fmt.Fprintf(out,
				"started pid=%d data-dir=%s\nPostgreSQL: 127.0.0.1:%d database=auriga (credentials in %s)\nNATS: nats://127.0.0.1:%d (loopback + auth; see %s)\nService roles: %s\n",
				state.PID, o.DataDir, state.PGPort, filepath.Join(o.DataDir, "credentials.env"),
				state.NATSPort, filepath.Join(o.DataDir, "nats.env"), serviceRolesPath(o.DataDir),
			)
			return err
		}
		if !processAlive(cmd.Process.Pid) {
			return fmt.Errorf("serve exited before becoming ready; see %s", logPath(o.DataDir))
		}
		select {
		case <-ctx.Done():
			_ = requestStop(o.DataDir, cmd.Process.Pid)
			return ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
	_ = requestStop(o.DataDir, cmd.Process.Pid)
	return fmt.Errorf("timed out waiting for serve readiness; see %s", logPath(o.DataDir))
}

func requestStop(dataDir string, pid int) error {
	if err := os.WriteFile(stopPath(dataDir), []byte("stop\n"), 0600); err != nil {
		return err
	}
	_ = signalStop(pid)
	return nil
}

func runStop(ctx context.Context, dataDir string, out io.Writer) error {
	if dataDir == "" {
		return errors.New("data directory is required")
	}
	abs, err := filepath.Abs(dataDir)
	if err != nil {
		return err
	}
	dataDir = abs
	state, running, err := runningFromState(dataDir)
	if err != nil {
		return err
	}
	if !running {
		_ = clearStaleRuntime(dataDir)
		_, err := fmt.Fprintln(out, "already stopped")
		return err
	}
	if err := requestStop(dataDir, state.PID); err != nil {
		return err
	}
	deadline := time.Now().Add(stopGracePeriod)
	for time.Now().Before(deadline) {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !processAlive(state.PID) {
			_ = removeStateFiles(dataDir)
			_ = os.Remove(lockPath(dataDir))
			_, err := fmt.Fprintf(out, "stopped pid=%d\n", state.PID)
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
	if p, err := os.FindProcess(state.PID); err == nil {
		_ = p.Kill()
	}
	deadline = time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if !processAlive(state.PID) {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	_ = removeStateFiles(dataDir)
	_ = os.Remove(lockPath(dataDir))
	if processAlive(state.PID) {
		return fmt.Errorf("process %d did not exit", state.PID)
	}
	_, err = fmt.Fprintf(out, "stopped pid=%d (forced)\n", state.PID)
	return err
}

func runStatus(dataDir string, out io.Writer) error {
	if dataDir == "" {
		return errors.New("data directory is required")
	}
	abs, err := filepath.Abs(dataDir)
	if err != nil {
		return err
	}
	dataDir = abs
	state, running, err := runningFromState(dataDir)
	if err != nil {
		return err
	}
	if !running {
		if _, err := fmt.Fprintf(out, "Status: stopped\nData directory: %s\n", dataDir); err != nil {
			return err
		}
		return errStatusStopped{}
	}
	_, err = fmt.Fprintf(out,
		"Status: running\nPID: %d\nData directory: %s\nPostgreSQL: 127.0.0.1:%d database=auriga user=auriga_dev\nNATS: nats://127.0.0.1:%d\nCredentials: %s\nService roles: %s\nStarted: %s\n",
		state.PID, dataDir, state.PGPort, state.NATSPort,
		filepath.Join(dataDir, "credentials.env"),
		serviceRolesPath(dataDir),
		state.StartedAt.Format(time.RFC3339),
	)
	return err
}

type errStatusStopped struct{}

func (errStatusStopped) Error() string { return "stopped" }

func isStatusStopped(err error) bool {
	var target errStatusStopped
	return errors.As(err, &target)
}
