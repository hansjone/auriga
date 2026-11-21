package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type custodyItem struct {
	Name, Path, Note string
}

// backupDir 与 backup/upgrade 使用同一位置：数据目录同级的 auriga-backups。
func backupDir(s state) string {
	return filepath.Join(filepath.Dir(s.Data), "auriga-backups")
}

func custodyItems(s state) []custodyItem {
	return []custodyItem{
		{"应急账号口令与 TOTP", filepath.Join(s.Data, "enrollment/emergency-enrollment.json"), "服务器私有文件（uid10001），doctor 自动登录也依赖它，不要拷出或删除；请现在把 TOTP 录入验证器、账号口令离线留存；之后可在安装目录用私密终端执行 sh deploy-nas/scripts/emergency.sh show 再次查看"},
		{"资源公钥根 resource-roots.json", filepath.Join(s.Data, "resource-keys/resource-roots.json"), "只含公钥；送回桌面构建机用于签发和校验资源，请复制并留档"},
		{"离线降级私钥 downgrade.pk8", filepath.Join(s.Data, "resource-keys/offline/downgrade.pk8"), "私钥；导出到受控离线介质保管，服务器上的副本仅 root 可读"},
		{"站点构建私钥目录", filepath.Join(s.Data+".oneclick", "site-build-key"), "含 site-private.pem（私钥，仅 root）；服务器本机构建使用。独立 Linux 构建机另行 keygen site，公钥用 trust add-site-key 登记"},
		{"备份目录", backupDir(s), "backup/upgrade 生成的备份和旧版本归档；含敏感材料，请定期拷贝到受控介质"},
	}
}

func distributionFingerprint(s state) string {
	if s.PublicKey == "" {
		return "无（未签名安装，仅限受控演练）"
	}
	fp, err := publicFingerprint(s.PublicKey)
	if err != nil {
		return "无法读取（" + err.Error() + "）"
	}
	return fp
}

type accessSummary struct {
	Mode         string
	PortalOrigin summaryOrigin
	AdminOrigin  summaryOrigin
}
type summaryOrigin struct {
	Scheme, Host string
	Port         int
}

func (o summaryOrigin) String() string {
	if o.Host == "" {
		return "未知"
	}
	return fmt.Sprintf("%s://%s:%d/", o.Scheme, o.Host, o.Port)
}

func printCustody(w io.Writer, s state) {
	fmt.Fprintln(w, "\n⚠ 请立即保管以下材料（安装摘要不含任何密钥内容，仅记录位置）")
	fmt.Fprintf(w, "  发行公钥指纹 SHA256(SPKI)：%s\n", distributionFingerprint(s))
	for i, item := range custodyItems(s) {
		fmt.Fprintf(w, "  %d. %s\n", i+1, item.Name)
		if item.Path != "" {
			fmt.Fprintf(w, "     位置：%s\n", item.Path)
		}
		fmt.Fprintf(w, "     说明：%s\n", item.Note)
	}
	fmt.Fprintf(w, "  安装摘要：%s\n", filepath.Join(s.Data, installSummaryName))
}

const installSummaryName = "INSTALL-SUMMARY.txt"

func installSummaryText(s state, access accessSummary, now time.Time) string {
	version := "未知"
	var r release
	if err := readJSONLoose(filepath.Join(s.Package, "offline-release.json"), &r); err == nil && r.Version != "" {
		version = r.Version
	}
	mode := access.Mode
	if mode == "" {
		mode = "未知"
	}
	var b strings.Builder
	b.WriteString("Auriga 安装摘要（不含任何口令或私钥内容）\n")
	fmt.Fprintf(&b, "版本：%s\n", version)
	fmt.Fprintf(&b, "安装/刷新时间：%s\n", now.UTC().Format(time.RFC3339))
	fmt.Fprintf(&b, "访问模式：%s\n", mode)
	fmt.Fprintf(&b, "管理台地址：%s\n", access.AdminOrigin)
	fmt.Fprintf(&b, "门户地址：%s\n", access.PortalOrigin)
	fmt.Fprintf(&b, "数据目录：%s\n", s.Data)
	fmt.Fprintf(&b, "发行公钥指纹 SHA256(SPKI)：%s\n", distributionFingerprint(s))
	b.WriteString("\n必须保管的材料：\n")
	for i, item := range custodyItems(s) {
		fmt.Fprintf(&b, "%d. %s\n", i+1, item.Name)
		if item.Path != "" {
			fmt.Fprintf(&b, "   位置：%s\n", item.Path)
		}
		fmt.Fprintf(&b, "   说明：%s\n", item.Note)
	}
	return b.String()
}

func writeInstallSummary(s state, access accessSummary, now time.Time) error {
	path := filepath.Join(s.Data, installSummaryName)
	if info, err := os.Lstat(path); err == nil && !info.Mode().IsRegular() {
		return fmt.Errorf("%s 不是普通文件，拒绝覆盖", path)
	}
	tmp := path + ".new"
	_ = os.Remove(tmp)
	file, err := os.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, err = file.WriteString(installSummaryText(s, access, now))
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(tmp, path)
	}
	if err != nil {
		_ = os.Remove(tmp)
	}
	return err
}
