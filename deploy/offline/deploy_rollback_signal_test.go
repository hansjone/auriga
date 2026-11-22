package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// rollback 在取消后仍经 finish 写收据：拦截 deployRun 返回 ctx.Err()，
// 断言 doRollback 因取消失败，且 run.finish(err) 把脱敏收据写入 logs.dir。
func TestRollbackCancelledContextStillFinishesReceipt(t *testing.T) {
	stubTrustedSource(t)
	migs := map[string]string{"auriga/flyway": "1:1:abc"}
	s := deployTestSite(t,
		deployRecord{Seq: 1, Status: recordOK, Images: imagesFor("1"), Migrations: migs, Formats: map[string]string{}},
		deployRecord{Seq: 2, Status: recordOK, Images: imagesFor("2")})
	writeTestRelease(t, &s, 1)
	stubDataVersions(t, &dataVersions{Migrations: migs, Formats: map[string]string{}})
	var sawCanceled bool
	oldRun := deployRun
	deployRun = func(ctx context.Context, _ runSpec) error {
		if err := ctx.Err(); err != nil {
			sawCanceled = errors.Is(err, context.Canceled)
			return err
		}
		return nil
	}
	t.Cleanup(func() { deployRun = oldRun })

	logsDir := t.TempDir()
	receiptPath := filepath.Join(logsDir, "rollback.json")
	cfg := &siteConfig{Server: &siteServer{Data: s.Data}, Logs: &siteLogs{Dir: logsDir}}
	var out bytes.Buffer
	run := &siteRun{
		receipt:     &siteReceipt{Command: "rollback", Args: []string{"--to", "1", "--json"}, Notes: []string{}},
		out:         &out,
		receiptPath: receiptPath,
		asJSON:      true,
		started:     time.Now(),
		stop:        func() {},
	}

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	site := &fakeSite{healthy: func(int) error { return nil }}
	err := doRollback(cancelled, site.ops(), cfg, s.Data, 1, run)
	if !sawCanceled || exitCodeOf(err) != 1 {
		t.Fatalf("已取消的 ctx 应传给子命令并使 doRollback 退出码 1：sawCanceled=%v err=%v", sawCanceled, err)
	}
	finErr := run.finish(err)
	if exitCodeOf(finErr) != 1 {
		t.Fatalf("finish 应保留退出码 1：%v", finErr)
	}
	if run.receipt.Phase != "rollback" || run.receipt.Command != "rollback" {
		t.Fatalf("收据 phase/command 错误：%+v", run.receipt)
	}
	if run.receipt.ExitCode == 0 {
		t.Fatalf("取消失败的收据退出码不应为 0：%+v", run.receipt)
	}
	rawFile, err := os.ReadFile(receiptPath)
	if err != nil {
		t.Fatalf("logs.dir 应写入收据文件：%v", err)
	}
	var saved siteReceipt
	if err := json.Unmarshal(rawFile, &saved); err != nil {
		t.Fatalf("落盘收据应为合法 JSON：%v\n%s", err, rawFile)
	}
	if saved.Phase != "rollback" || saved.ExitCode == 0 {
		t.Fatalf("落盘收据字段不符：%+v", saved)
	}
	// finish 在 asJSON=true 时只往 run.terminal（或 os.Stdout）写 JSON，不混入 run.out。
	_ = out
}

func TestDeployCleanupContextNotCancelled(t *testing.T) {
	ctx, cancel := deployCleanupContext()
	defer cancel()
	select {
	case <-ctx.Done():
		t.Fatal("cleanup 上下文不应立刻取消")
	case <-time.After(20 * time.Millisecond):
	}
	if _, ok := ctx.Deadline(); !ok {
		t.Fatal("cleanup 上下文须带超时")
	}
	dead, stop := context.WithCancel(context.Background())
	stop()
	if dead.Err() == nil {
		t.Fatal("对照：已取消的父上下文应有错")
	}
	c2, c2cancel := deployCleanupContext()
	defer c2cancel()
	if errors.Is(c2.Err(), context.Canceled) {
		t.Fatal("独立 cleanup 不应继承取消")
	}
}
