package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestBackupDatabasesRecordsReceipt(t *testing.T) {
	stubTrustedSource(t)
	s := deployTestSite(t)
	oldQuery := psqlQuery
	psqlQuery = func(_ state, db, sql string) (string, error) {
		if strings.Contains(sql, "datname") {
			return "auriga\ncollab-db\naudit\nother", nil
		}
		return "", nil
	}
	t.Cleanup(func() { psqlQuery = oldQuery })

	oldDump := pgDump
	pgDump = func(_ state, db, outFile string) error {
		return os.WriteFile(outFile, []byte("dump-"+db), 0600)
	}
	t.Cleanup(func() { pgDump = oldDump })

	run, _ := testRun()
	var buf bytes.Buffer
	if err := backupDatabases(s, run.receipt, &buf); err != nil {
		t.Fatal(err)
	}
	if len(run.receipt.DBBackups) != 3 {
		t.Fatalf("应备份 3 个库：%+v", run.receipt.DBBackups)
	}
	for _, b := range run.receipt.DBBackups {
		if b.SHA256 == "" || b.Path == "" {
			t.Fatalf("收据缺摘要或路径：%+v", b)
		}
		raw, err := os.ReadFile(b.Path)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(raw), b.Database) {
			t.Fatalf("文件内容不对：%s", raw)
		}
		if runtime.GOOS != "windows" {
			st, err := os.Stat(b.Path)
			if err != nil {
				t.Fatal(err)
			}
			if st.Mode().Perm()&0077 != 0 {
				t.Fatalf("备份文件权限应仅所有者：%v", st.Mode())
			}
		}
	}
	if !strings.Contains(buf.String(), "pg_dump auriga") {
		t.Fatalf("应打印进度：%s", buf.String())
	}
	if runtime.GOOS != "windows" {
		dir := filepath.Dir(run.receipt.DBBackups[0].Path)
		st, err := os.Stat(dir)
		if err != nil {
			t.Fatal(err)
		}
		if st.Mode().Perm()&0077 != 0 {
			t.Fatalf("备份目录权限应仅所有者：%v", st.Mode())
		}
	}
}

// 生产 pgDump：输出流式写文件（0600），口令不在 Go 侧出现；失败删除半截文件。
func TestPgDumpStreamsAndCleansUp(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("需要 sh")
	}
	pkg := t.TempDir()
	script := filepath.Join(pkg, "deploy-nas", "scripts", "oneclick-compose.sh")
	if err := os.MkdirAll(filepath.Dir(script), 0755); err != nil {
		t.Fatal(err)
	}
	s := state{Data: t.TempDir(), Package: pkg}
	write := func(body string) {
		if err := os.WriteFile(script, []byte(body), 0755); err != nil {
			t.Fatal(err)
		}
	}
	write("#!/bin/sh\nprintf 'ARGS:%s\n' \"$*\"\nprintf 'CREATE TABLE x();\n'\n")
	out := filepath.Join(t.TempDir(), "auriga.sql")
	if err := pgDump(s, "auriga", out); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "CREATE TABLE x();") || !strings.Contains(string(raw), "pg_bootstrap_password") || strings.Contains(string(raw), "PGPASSWORD=e") {
		t.Fatalf("转储内容不对（口令应只从容器内 secret 文件读）：%s", raw)
	}
	if st, _ := os.Stat(out); st.Mode().Perm() != 0600 {
		t.Fatalf("转储文件应 0600：%v", st.Mode())
	}
	if err := pgDump(s, "auriga", out); err == nil {
		t.Fatal("已存在的文件不应被覆盖（O_EXCL）")
	}
	write("#!/bin/sh\nprintf 'partial'\nexit 1\n")
	bad := filepath.Join(t.TempDir(), "bad.sql")
	if err := pgDump(s, "auriga", bad); err == nil {
		t.Fatal("pg_dump 失败应报错")
	}
	if _, err := os.Lstat(bad); !os.IsNotExist(err) {
		t.Fatalf("失败时应删除半截文件：%v", err)
	}
}

func TestBackupDatabasesRefusesUntrustedRootAndDumpFailure(t *testing.T) {
	s := deployTestSite(t)
	oldQuery := psqlQuery
	psqlQuery = func(_ state, db, sql string) (string, error) { return "auriga\naudit", nil }
	t.Cleanup(func() { psqlQuery = oldQuery })
	old := depsTrustedSourceCheck
	depsTrustedSourceCheck = func(what, path string) error { return errors.New(what + path + " 不是 root 所有") }
	if err := backupDatabases(s, nil, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "备份目录") {
		t.Fatalf("备份根目录不受信应拒绝：%v", err)
	}
	depsTrustedSourceCheck = func(string, string) error { return nil }
	t.Cleanup(func() { depsTrustedSourceCheck = old })
	oldDump := pgDump
	pgDump = func(_ state, db, outFile string) error {
		if db == "audit" {
			return errors.New("连接失败")
		}
		return os.WriteFile(outFile, []byte("ok"), 0600)
	}
	t.Cleanup(func() { pgDump = oldDump })
	rc := &siteReceipt{}
	if err := backupDatabases(s, rc, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "audit") {
		t.Fatalf("任一库转储失败应整体失败（阻止部署）：%v", err)
	}
	if len(rc.DBBackups) != 0 {
		t.Fatalf("失败时收据不应记成功的备份列表：%+v", rc.DBBackups)
	}
}
