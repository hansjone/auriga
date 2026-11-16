package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
)

type buildBudget struct {
	MemoryMiB int
	CPUs      int
}

func localDockerEndpoint(endpoint string) error {
	if strings.HasPrefix(endpoint, "unix:///") || strings.HasPrefix(strings.ToLower(endpoint), "npipe:////./pipe/") {
		return nil
	}
	return errors.New("构建/安装只允许本机Docker Unix socket或Windows命名管道；拒绝远程TCP/SSH context，请选择本机Engine")
}

func (b buildBudget) dockerArgs() []string {
	m := strconv.Itoa(b.MemoryMiB) + "m"
	return []string{"--memory", m, "--memory-swap", m, "--cpus", strconv.Itoa(b.CPUs)}
}
func validateBuildBudget(b buildBudget, total, available uint64) error {
	if b.MemoryMiB < 1024 || b.MemoryMiB > 1<<20 || b.CPUs < 1 || b.CPUs > 128 {
		return errors.New("构建限制需要 --memory-mib >=1024、--cpus 1..128；默认4096MiB/2CPU")
	}
	required := (uint64(b.MemoryMiB) + 1024) << 20
	if total < required || available < required {
		return fmt.Errorf("可用构建内存不足：Docker总内存%dMiB，可用%dMiB，至少需要%dMiB（含保留给运行服务的1024MiB）；低峰重试或用独立构建机。Windows请检查%%UserProfile%%\\.wslconfig [wsl2] memory=8GB/processors=2，调整后在所有任务停止时由用户重启WSL", total>>20, available>>20, required>>20)
	}
	return nil
}
func buildEnvironment(b buildBudget) error {
	if err := dockerReady(); err != nil {
		return err
	}
	raw, err := capture("docker", "info", "--format", "{{json .}}")
	if err != nil {
		return err
	}
	var info struct {
		MemTotal    uint64
		OSType      string
		MemoryLimit bool
		CPUCfsQuota bool `json:"CpuCfsQuota"`
	}
	if err := json.Unmarshal([]byte(raw), &info); err != nil {
		return err
	}
	if info.OSType != "linux" {
		return errors.New("构建需要Docker Linux容器后端（WSL2）；当前不是Linux Engine")
	}
	if !info.MemoryLimit || !info.CPUCfsQuota {
		return errors.New("当前Linux Engine缺少内存/CPU配额控制，拒绝无保护构建；请使用支持cgroup限制的Engine或独立构建机")
	}
	available := info.MemTotal
	if runtime.GOOS == "linux" {
		text, err := os.ReadFile("/proc/meminfo")
		if err != nil {
			return err
		}
		found := false
		for _, line := range strings.Split(string(text), "\n") {
			fields := strings.Fields(line)
			if len(fields) >= 2 && fields[0] == "MemAvailable:" {
				kb, err := strconv.ParseUint(fields[1], 10, 64)
				if err != nil {
					return err
				}
				available = kb << 10
				found = true
				break
			}
		}
		if !found {
			return errors.New("无法读取MemAvailable，拒绝在资源未知时构建")
		}
	}
	if err := validateBuildBudget(b, info.MemTotal, available); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "PASS 构建资源：Docker %dMiB，可用%dMiB；每个工具容器上限%dMiB/%dCPU，预留1024MiB\n", info.MemTotal>>20, available>>20, b.MemoryMiB, b.CPUs)
	return nil
}

func buildDoctor(args []string) error {
	f := flags("doctor --build")
	m := f.Int("memory-mib", 4096, "")
	c := f.Int("cpus", 2, "")
	if err := f.Parse(args); err != nil {
		return err
	}
	if f.NArg() != 0 {
		return errors.New("多余构建诊断参数")
	}
	return buildEnvironment(buildBudget{MemoryMiB: *m, CPUs: *c})
}
