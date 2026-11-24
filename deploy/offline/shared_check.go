package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

type sharedRuntimeReport struct {
	Present             bool
	StorageBackend      string
	QuotaMount          string
	QuotaBinary         string
	ReplayReady         bool
	XFSBinaryChecked    bool
	XFSBinaryAvailable  bool
	XFSBinaryCheckError string
	Warnings            []string
}

var sharedXFSQuotaProbe = defaultSharedXFSQuotaProbe

func defaultSharedXFSQuotaProbe(agentImage, quotaBinary string) error {
	if strings.TrimSpace(agentImage) == "" {
		return fmt.Errorf("agent image reference empty")
	}
	cmd := exec.Command("docker", "run", "--rm", "--pull", "never", "--network", "none", "--entrypoint", "sh", agentImage, "-c", `test -x "$1"`, "--", quotaBinary)
	if out, err := cmd.CombinedOutput(); err != nil {
		msg := strings.TrimSpace(string(out))
		if msg != "" {
			return fmt.Errorf("%v: %s", err, msg)
		}
		return err
	}
	return nil
}

func checkSharedRuntime(s state) sharedRuntimeReport {
	report := sharedRuntimeReport{
		StorageBackend: "unknown",
	}
	if s.Data == "" {
		return report
	}
	for _, rel := range []string{"config/shared-source", "config/shared-runtime", "config/shared-replay-source", "agent-tokens"} {
		if pathExists(filepath.Join(s.Data, rel)) {
			report.Present = true
			break
		}
	}
	if !report.Present {
		return report
	}

	if receiptPath := latestSharedReceiptPath(filepath.Join(s.Data, "config/shared-source")); receiptPath != "" {
		var receipt struct {
			StorageBackend string `json:"storageBackend"`
			QuotaMount     string `json:"quotaMount"`
			QuotaBinary    string `json:"quotaBinary"`
		}
		if err := readJSONLoose(receiptPath, &receipt); err == nil {
			backend := strings.TrimSpace(receipt.StorageBackend)
			if backend == "" {
				backend = "ext4-image"
			}
			report.StorageBackend = backend
			report.QuotaMount = strings.TrimSpace(receipt.QuotaMount)
			report.QuotaBinary = strings.TrimSpace(receipt.QuotaBinary)
			if report.StorageBackend == "xfs" && report.QuotaBinary == "" {
				report.QuotaBinary = "/usr/sbin/xfs_quota"
			}
		} else {
			report.Warnings = append(report.Warnings, "无法读取 shared-source installation.receipt（可能需 root 权限）")
		}
	} else {
		report.Warnings = append(report.Warnings, "未找到可读的 shared-source/v-*/installation.receipt")
	}

	replayDir := filepath.Join(s.Data, "config/shared-replay-source")
	if pathExists(filepath.Join(replayDir, "bucket.receipt")) {
		var act struct {
			Open bool `json:"open"`
		}
		if err := readJSONLoose(filepath.Join(replayDir, "activation.receipt"), &act); err == nil && act.Open {
			report.ReplayReady = true
		}
	}
	if !report.ReplayReady {
		report.Warnings = append(report.Warnings, "replay 激活回执未就绪或无权读取（需确认已完成 start.sh 的 replay 预建与撤权）")
	}

	if report.StorageBackend == "xfs" {
		if report.QuotaMount == "" {
			report.Warnings = append(report.Warnings, "xfs 后端缺少 quotaMount 配置")
		}
		agentImage := strings.TrimSpace(s.Images["AGENT"])
		if agentImage == "" {
			envVals := readNonSecretStackEnvKeys(filepath.Join(s.Data, "stack.env"), map[string]bool{
				"AURIGA_IMAGE_AGENT": true,
			})
			agentImage = strings.TrimSpace(envVals["AURIGA_IMAGE_AGENT"])
		}
		report.XFSBinaryChecked = true
		if err := sharedXFSQuotaProbe(agentImage, report.QuotaBinary); err != nil {
			report.XFSBinaryAvailable = false
			report.XFSBinaryCheckError = err.Error()
			report.Warnings = append(report.Warnings, fmt.Sprintf("agent 镜像缺少可执行 %s（%s），首次成员配额将失败", report.QuotaBinary, err.Error()))
		} else {
			report.XFSBinaryAvailable = true
		}
	}

	return report
}

func latestSharedReceiptPath(sourceDir string) string {
	entries, err := os.ReadDir(sourceDir)
	if err != nil {
		return ""
	}
	var dirs []string
	for _, entry := range entries {
		if entry.IsDir() && strings.HasPrefix(entry.Name(), "v-") {
			dirs = append(dirs, entry.Name())
		}
	}
	if len(dirs) == 0 {
		return ""
	}
	sort.Strings(dirs)
	return filepath.Join(sourceDir, dirs[len(dirs)-1], "installation.receipt")
}

func (r sharedRuntimeReport) DoctorLine() string {
	if !r.Present {
		return "SKIP 共享模式运行态 — 未启用"
	}
	replay := "not_ready"
	if r.ReplayReady {
		replay = "ready"
	}
	detail := fmt.Sprintf("backend=%s replay=%s", r.StorageBackend, replay)
	if r.StorageBackend == "xfs" {
		xfsStatus := "missing"
		if r.XFSBinaryAvailable {
			xfsStatus = "ok"
		}
		detail = fmt.Sprintf("%s quotaMount=%s quotaBinary=%s(%s)", detail, r.QuotaMount, r.QuotaBinary, xfsStatus)
	}
	if len(r.Warnings) > 0 {
		return fmt.Sprintf("WARN 共享模式运行态 — %s；告警：%s", detail, strings.Join(r.Warnings, "；"))
	}
	return fmt.Sprintf("PASS 共享模式运行态 — %s", detail)
}
