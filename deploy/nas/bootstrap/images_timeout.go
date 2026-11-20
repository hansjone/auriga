package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync/atomic"
	"time"
)

const (
	imageStallTimeoutEnv = "AURIGA_DOCKER_IMAGE_STALL_TIMEOUT"
	imageSaveMaxEnv      = "AURIGA_DOCKER_IMAGE_SAVE_MAX"
	imageInspectEnv      = "AURIGA_DOCKER_INSPECT_TIMEOUT"

	defaultImageStallTimeout = 10 * time.Minute
	defaultImageSaveMax      = 2 * time.Hour
	defaultImageInspect      = 10 * time.Minute
	minImageTimeout          = time.Minute
	maxImageTimeout          = 24 * time.Hour
)

// imageTimeouts 是 docker image save / inspect 的时限。save 不用总时长截断：
// 只要管道持续有数据就续期，连续 Stall 没有任何字节才中止；SaveMax 只作兜底。
type imageTimeouts struct {
	Stall, SaveMax, Inspect time.Duration
}

// imageEnvLookup 可在测试中替换。
var imageEnvLookup = os.LookupEnv

func parseImageTimeoutEnv(lookup func(string) (string, bool), name string, def time.Duration) (time.Duration, error) {
	raw, ok := lookup(name)
	if !ok || raw == "" {
		return def, nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("环境变量 %s 的值 %q 不是有效的时长；请写成 Go 时长，如 30m、2h", name, raw)
	}
	if d <= 0 || d < minImageTimeout || d > maxImageTimeout {
		return 0, fmt.Errorf("环境变量 %s 的值 %q 超出范围；须在 %s 到 %s 之间", name, raw, minImageTimeout, maxImageTimeout)
	}
	return d, nil
}

func loadImageTimeouts(lookup func(string) (string, bool)) (imageTimeouts, error) {
	var t imageTimeouts
	var err error
	if t.Stall, err = parseImageTimeoutEnv(lookup, imageStallTimeoutEnv, defaultImageStallTimeout); err != nil {
		return t, err
	}
	if t.SaveMax, err = parseImageTimeoutEnv(lookup, imageSaveMaxEnv, defaultImageSaveMax); err != nil {
		return t, err
	}
	if t.Inspect, err = parseImageTimeoutEnv(lookup, imageInspectEnv, defaultImageInspect); err != nil {
		return t, err
	}
	return t, nil
}

// stallReader 只在阻塞读取期间计时：读到数据即解除，下一次读取重新开始计时。
type stallReader struct {
	r       io.Reader
	timer   *time.Timer
	stall   time.Duration
	stalled *atomic.Bool
}

func (s *stallReader) Read(p []byte) (int, error) {
	s.timer.Reset(s.stall)
	n, err := s.r.Read(p)
	s.timer.Stop()
	return n, err
}

// runImageSave 执行 cmd（其 Stdout 由本函数接管），把输出交给 verify。
// stall 为连续无字节的上限，saveMax 为总时长上限；时长可注入以便测试。
func runImageSave(ctx context.Context, cmd func(context.Context) *exec.Cmd, verify func(io.Reader) error, stall, saveMax time.Duration) error {
	start := time.Now()
	parent := ctx
	ctx, cancel := context.WithTimeout(parent, saveMax)
	defer cancel()
	c := cmd(ctx)
	c.Stderr = io.Discard
	c.WaitDelay = 2 * time.Second
	pipe, err := c.StdoutPipe()
	if err != nil {
		return err
	}
	if err := c.Start(); err != nil {
		_ = pipe.Close()
		return err
	}
	var stalled atomic.Bool
	timer := time.AfterFunc(time.Hour, func() {
		stalled.Store(true)
		cancel()
	})
	timer.Stop()
	defer timer.Stop()
	err = verify(&stallReader{r: pipe, timer: timer, stall: stall, stalled: &stalled})
	contextErr := ctx.Err()
	isStalled := stalled.Load()
	if err != nil {
		cancel()
	}
	_ = pipe.Close()
	waitErr := c.Wait()
	if contextErr != nil || isStalled {
		elapsed := time.Since(start).Round(time.Second)
		switch {
		case isStalled:
			return fmt.Errorf("导出镜像（docker image save）失败：连续 %s 没有读到任何数据，已用时 %s；现场负载高或磁盘慢时可调大 %s: %w", stall, elapsed, imageStallTimeoutEnv, context.DeadlineExceeded)
		case parent.Err() == nil:
			return fmt.Errorf("导出镜像（docker image save）失败：总用时超过上限 %s，已用时 %s；现场负载高或磁盘慢时可调大 %s: %w", saveMax, elapsed, imageSaveMaxEnv, contextErr)
		default:
			return fmt.Errorf("导出镜像（docker image save）被中断，已用时 %s: %w", elapsed, contextErr)
		}
	}
	if err != nil {
		return err
	}
	if waitErr != nil {
		return fmt.Errorf("导出镜像（docker image save）失败: %w", waitErr)
	}
	return nil
}
