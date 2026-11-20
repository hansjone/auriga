package main

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func shellSave(script string) func(context.Context) *exec.Cmd {
	return func(c context.Context) *exec.Cmd { return exec.CommandContext(c, "sh", "-c", script) }
}

func skipNoSh(t *testing.T) {
	t.Helper()
	if os.PathSeparator == '\\' {
		t.Skip("POSIX executable fixture")
	}
}

func TestImageSaveSteadyDataDoesNotStall(t *testing.T) {
	skipNoSh(t)
	// 总时长约 1.2 秒，远超 0.5 秒的无进展阈值，但持续有数据，不应中止。
	script := "for i in 1 2 3 4 5 6; do printf x; sleep 0.2; done"
	var got int
	err := runImageSave(context.Background(), shellSave(script), func(r io.Reader) error {
		b, err := io.ReadAll(r)
		got = len(b)
		return err
	}, 500*time.Millisecond, time.Minute)
	if err != nil || got != 6 {
		t.Fatalf("持续有数据不应超时: got=%d err=%v", got, err)
	}
}

func TestImageSaveStallAborts(t *testing.T) {
	skipNoSh(t)
	start := time.Now()
	err := runImageSave(context.Background(), shellSave("printf x; sleep 30"), func(r io.Reader) error {
		_, err := io.Copy(io.Discard, r)
		return err
	}, 300*time.Millisecond, time.Minute)
	if err == nil || !strings.Contains(err.Error(), imageStallTimeoutEnv) || !strings.Contains(err.Error(), "没有读到任何数据") || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("应报无进展超时: %v", err)
	}
	if time.Since(start) > 10*time.Second {
		t.Fatal("中止过慢")
	}
}

func TestImageSaveTotalCap(t *testing.T) {
	skipNoSh(t)
	script := "while true; do printf x; sleep 0.05; done"
	err := runImageSave(context.Background(), shellSave(script), func(r io.Reader) error {
		_, err := io.Copy(io.Discard, r)
		return err
	}, 5*time.Second, 400*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), imageSaveMaxEnv) || !strings.Contains(err.Error(), "总用时超过上限") {
		t.Fatalf("应报总上限: %v", err)
	}
}

func TestImageInspectTimeoutMessage(t *testing.T) {
	skipNoSh(t)
	dir := t.TempDir()
	if err := os.WriteFile(dir+"/docker", []byte("#!/bin/sh\nexec sleep 30\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	_, err := inspectDockerImageWith(context.Background(), "x", 300*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), imageInspectEnv) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("应报 inspect 超时: %v", err)
	}
}

func TestLoadImageTimeouts(t *testing.T) {
	look := func(m map[string]string) func(string) (string, bool) {
		return func(k string) (string, bool) { v, ok := m[k]; return v, ok }
	}
	d, err := loadImageTimeouts(look(nil))
	if err != nil || d.Stall != 10*time.Minute || d.SaveMax != 2*time.Hour || d.Inspect != 10*time.Minute {
		t.Fatalf("默认值错误: %+v %v", d, err)
	}
	d, err = loadImageTimeouts(look(map[string]string{imageStallTimeoutEnv: "30m", imageSaveMaxEnv: "5h", imageInspectEnv: "1m"}))
	if err != nil || d.Stall != 30*time.Minute || d.SaveMax != 5*time.Hour || d.Inspect != time.Minute {
		t.Fatalf("覆盖失败: %+v %v", d, err)
	}
	for _, name := range []string{imageStallTimeoutEnv, imageSaveMaxEnv, imageInspectEnv} {
		for _, v := range []string{"abc", "-5m", "0", "30s", "25h", "10"} {
			_, err := loadImageTimeouts(look(map[string]string{name: v}))
			if err == nil || !strings.Contains(err.Error(), name) {
				t.Fatalf("%s=%q 应被拒绝: %v", name, v, err)
			}
		}
	}
}
