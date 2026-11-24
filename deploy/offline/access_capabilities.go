package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type accessCapabilitiesReport struct {
	Mode                    string
	PortalScheme            string
	ManagedNetworkState     string
	ManagedNetworkReason    string
	CollabConfigured        bool
	SharedRuntimeConfigured bool
	DisabledCapabilities    []string
}

func accessCapabilities(s state) accessCapabilitiesReport {
	report := accessCapabilitiesReport{
		Mode:                "unknown",
		PortalScheme:        "unknown",
		ManagedNetworkState: "unconfigured",
	}

	accessPath := strings.TrimSpace(s.Access)
	if accessPath == "" && s.Data != "" {
		accessPath = filepath.Join(s.Data, "config/materials/access.json")
	}
	if accessPath != "" {
		var access struct {
			Mode         string `json:"mode"`
			PortalOrigin struct {
				Scheme string `json:"scheme"`
			} `json:"portalOrigin"`
		}
		if err := readJSONLoose(accessPath, &access); err == nil {
			if m := strings.TrimSpace(access.Mode); m != "" {
				report.Mode = m
			}
			if sc := strings.ToLower(strings.TrimSpace(access.PortalOrigin.Scheme)); sc != "" {
				report.PortalScheme = sc
			}
		}
	}

	var managedOrigin string
	if s.Data != "" {
		envVals := readNonSecretStackEnvKeys(filepath.Join(s.Data, "stack.env"), map[string]bool{
			"AURIGA_MANAGED_PLATFORM_ORIGIN": true,
		})
		managedOrigin = strings.TrimSpace(envVals["AURIGA_MANAGED_PLATFORM_ORIGIN"])
		report.CollabConfigured = pathExists(filepath.Join(s.Data, "config/collab-source"))
		report.SharedRuntimeConfigured = pathExists(filepath.Join(s.Data, "config/shared-runtime"))
	}

	managedFilePresent := s.Data != "" && pathExists(filepath.Join(s.Data, "config/collab-runtime/platform/managed-network.json"))
	switch {
	case managedOrigin != "" && !strings.HasPrefix(managedOrigin, "https://"):
		report.ManagedNetworkState = "unconfigured"
		report.ManagedNetworkReason = "origin_not_https"
	case managedOrigin == "":
		report.ManagedNetworkState = "unconfigured"
		if report.Mode == "ip" || report.PortalScheme == "http" {
			report.ManagedNetworkReason = "access_mode_not_https_domain"
		} else {
			report.ManagedNetworkReason = "origin_empty"
		}
	case !managedFilePresent:
		report.ManagedNetworkState = "unconfigured"
		report.ManagedNetworkReason = "collab_material_absent"
	default:
		report.ManagedNetworkState = "configured"
		report.ManagedNetworkReason = ""
	}

	if report.ManagedNetworkState != "configured" {
		report.DisabledCapabilities = append(report.DisabledCapabilities, "托管网络（managed network）")
	}
	if !report.CollabConfigured {
		report.DisabledCapabilities = append(report.DisabledCapabilities, "collab 消息网络", "共享模式跨成员消息（sharedMessages）")
	}
	return report
}

// readNonSecretStackEnvKeys 只读取调用方显式列出的非敏感键，避免将凭据读入或输出。
func readNonSecretStackEnvKeys(path string, allow map[string]bool) map[string]string {
	out := make(map[string]string, len(allow))
	f, err := os.Open(path)
	if err != nil {
		return out
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		if !allow[k] {
			continue
		}
		v = strings.TrimSpace(v)
		if len(v) >= 2 && ((v[0] == '\'' && v[len(v)-1] == '\'') || (v[0] == '"' && v[len(v)-1] == '"')) {
			v = v[1 : len(v)-1]
		}
		out[k] = v
	}
	return out
}

func pathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func (r accessCapabilitiesReport) reasonDescription() string {
	switch r.ManagedNetworkReason {
	case "":
		return "已配置"
	case "access_mode_not_https_domain":
		return fmt.Sprintf("未配置：当前访问形态为 mode=%s/portal=%s，托管网络与 collab 默认要求 HTTPS 呈现入口，且 AURIGA_MANAGED_PLATFORM_ORIGIN 为空", r.Mode, r.PortalScheme)
	case "origin_empty":
		return "未配置：AURIGA_MANAGED_PLATFORM_ORIGIN 为空"
	case "origin_not_https":
		return "被门禁拒绝：AURIGA_MANAGED_PLATFORM_ORIGIN 非 https://（render-managed.sh 要求 HTTPS）"
	case "collab_material_absent":
		return "未完成激活：缺少 config/collab-runtime/platform/managed-network.json（需先执行 collab bootstrap）"
	default:
		return r.ManagedNetworkReason
	}
}

func (r accessCapabilitiesReport) DoctorLine() string {
	if r.Mode == "ip" || r.PortalScheme == "http" || len(r.DisabledCapabilities) > 0 {
		disabled := "无"
		if len(r.DisabledCapabilities) > 0 {
			disabled = strings.Join(r.DisabledCapabilities, "、")
		}
		return fmt.Sprintf(
			"WARN 访问形态与可选能力 — mode=%s portal=%s，managedNetworkState=%s（%s）；未启用能力：%s（共享模式核心能力不依赖托管网络/HTTPS）",
			r.Mode,
			r.PortalScheme,
			r.ManagedNetworkState,
			r.reasonDescription(),
			disabled,
		)
	}
	return fmt.Sprintf(
		"PASS 访问形态与可选能力 — mode=%s portal=%s，managedNetworkState=%s，collab 已激活",
		r.Mode,
		r.PortalScheme,
		r.ManagedNetworkState,
	)
}

func (r accessCapabilitiesReport) StatusLine() string {
	return fmt.Sprintf(
		"访问形态 mode=%s portal=%s；managedNetworkState=%s（%s）",
		r.Mode,
		r.PortalScheme,
		r.ManagedNetworkState,
		r.reasonDescription(),
	)
}
