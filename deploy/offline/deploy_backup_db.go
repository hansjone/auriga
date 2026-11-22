package main

// --backup-db：部署切换前对各库 pg_dump（不停服，设计 D2）。

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type receiptDBBackup struct {
	Database string `json:"database"`
	Path     string `json:"path"`
	SHA256   string `json:"sha256"`
}

// pgDump 把指定库导出到 outFile（可注入；生产实现走 postgres 容器内 pg_dump）。
// 口令只在容器内从 secret 文件读入 PGPASSWORD，不进命令行参数与日志；输出直接流式写文件
// （O_EXCL、0600），不在内存里缓冲整库。失败时删除半截文件。
var pgDump = func(s state, db, outFile string) error {
	script := `PGPASSWORD="$(cat /run/secrets/pg_bootstrap_password 2>/dev/null)" exec pg_dump -U auriga_bootstrap -d "$1" --no-owner --no-acl`
	f, err := os.OpenFile(outFile, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	err = composeTo(s, f, "exec", "-T", "postgres", "sh", "-c", script, "pg_dump", db)
	if serr := f.Sync(); err == nil {
		err = serr
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(outFile)
		return err
	}
	return os.Chmod(outFile, 0600)
}

// listDumpDatabases：主库 auriga 必选；collab-db、audit 若存在则纳入。
func listDumpDatabases(s state) ([]string, error) {
	dbs, err := psqlQuery(s, "auriga", "SELECT datname FROM pg_database WHERE NOT datistemplate AND datallowconn ORDER BY 1")
	if err != nil {
		return nil, fmt.Errorf("列出数据库失败：%w", err)
	}
	present := map[string]bool{}
	for _, name := range strings.Fields(dbs) {
		present[name] = true
	}
	if !present["auriga"] {
		return nil, fmt.Errorf("主库 auriga 不存在，无法 --backup-db")
	}
	out := []string{"auriga"}
	for _, name := range []string{"collab-db", "audit"} {
		if present[name] {
			out = append(out, name)
		}
	}
	return out, nil
}

// backupDatabases 在切换前 pg_dump；目录 root 风格 0700，文件 0600；收据记录路径与 sha256。
func backupDatabases(s state, rc *siteReceipt, w io.Writer) error {
	root := backupDir(s)
	if err := os.MkdirAll(root, 0700); err != nil {
		return err
	}
	if err := noSymlinks(root); err != nil {
		return err
	}
	// 备份根目录须 root 所有、组和其他用户不可写，否则别人可以替换或窥视转储。
	if err := depsTrustedSourceCheck("备份目录 ", root); err != nil {
		return err
	}
	dir := filepath.Join(root, "db-"+time.Now().UTC().Format("20060102T150405.000000000"))
	if err := os.Mkdir(dir, 0700); err != nil {
		return err
	}
	if err := os.Chmod(dir, 0700); err != nil {
		return err
	}
	dbs, err := listDumpDatabases(s)
	if err != nil {
		return err
	}
	var dumps []receiptDBBackup
	for _, db := range dbs {
		safe := strings.ReplaceAll(db, "/", "_")
		out := filepath.Join(dir, safe+".sql")
		fmt.Fprintf(w, "pg_dump %s → %s\n", db, out)
		if err := pgDump(s, db, out); err != nil {
			return fmt.Errorf("pg_dump %s 失败（未切换，已写的转储保留在 %s）：%w", db, dir, err)
		}
		sum, err := digest(out)
		if err != nil {
			return err
		}
		dumps = append(dumps, receiptDBBackup{Database: db, Path: out, SHA256: sum})
	}
	if rc != nil {
		rc.DBBackups = dumps
		rc.Notes = append(rc.Notes, fmt.Sprintf("--backup-db：已写入 %s（%d 个库；不停服，跨库不保证同一时刻）", dir, len(dumps)))
	}
	return nil
}
