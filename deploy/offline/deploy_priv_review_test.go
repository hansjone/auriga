package main

// 2a（下）独审修复的负例：未登记运行器时 sudo status 不收 --data、受限 deploy 提交号位置与
// parseDeploy 一致、add-release-key 不能给已固定 trustedKeys 的 state 加信任根、gpg 签名形态检查。

import (
	"strings"
	"testing"
)

func TestRestrictedStatusWhenSiteConfigPresent(t *testing.T) {
	stubRunnerUID(t, map[string]string{"runner": "1001"})
	admin := siteInvocation{Sudo: true, Root: true, SudoUID: "1000"}
	runner := siteInvocation{Sudo: true, Root: true, SudoUID: "1001"}
	// site.json 存在但没登记运行器：运行器被当成普通账户，status 仍不能收 --data。
	stubSudoConfig(t, &siteConfig{Server: &siteServer{Data: "/srv/auriga/auriga-data"}}, nil)
	for _, inv := range []siteInvocation{admin, runner} {
		handled, err := restrictedStatus([]string{"--data", "/home/runner/fake/auriga-data"}, inv)
		if !handled || err == nil || !strings.Contains(err.Error(), "受限") {
			t.Fatalf("site.json 存在时经 sudo 的 status --data 应被拒绝：handled=%t err=%v", handled, err)
		}
	}
	// 登记了运行器、管理员调用：同样不收参数。
	stubSudoConfig(t, runnerConfig("runner"), nil)
	if handled, err := restrictedStatus([]string{"--data", "/x/y/auriga-data"}, admin); !handled || err == nil {
		t.Fatalf("管理员经 sudo 的 status --data 也应受限：handled=%t err=%v", handled, err)
	}
	// 没有 site.json 的旧签名包站点：照旧交给原 status 处理。
	stubSudoConfig(t, nil, nil)
	if handled, err := restrictedStatus([]string{"--data", "/srv/auriga"}, admin); handled || err != nil {
		t.Fatalf("没有 site.json 时应按原有 status 处理：handled=%t err=%v", handled, err)
	}
}

func TestRestrictedDeployCommitPositionMatchesParse(t *testing.T) {
	c := strings.Repeat("c", 40)
	for _, args := range [][]string{{"--full", c, "--json"}, {"--services", "edge", c, "--dry-run"}} {
		if err := restrictedArgs("deploy", args); err == nil {
			t.Fatalf("提交号在中间应被白名单拒绝：%v", args)
		}
	}
	// 白名单放行的写法必须都能被 parseDeploy 解析出同一个提交号。
	for _, args := range [][]string{{c}, {c, "--full"}, {"--full", c}, {"--services", "edge,platform", c}, {c, "--services=edge", "--json"}, {"--dry-run", "--json", c}} {
		if err := restrictedArgs("deploy", args); err != nil {
			t.Fatalf("%v 应被白名单接受：%v", args, err)
		}
		o, err := parseDeploy(args)
		if err != nil || o.Commit != c {
			t.Fatalf("%v 白名单放行但 parseDeploy 结果不一致：%+v %v", args, o, err)
		}
	}
}

func TestPinReleaseKeyRefusedWhenTrustedKeysPinned(t *testing.T) {
	s := deployTestSite(t, deployRecord{Seq: 1, Status: recordOK, Images: imagesFor("1")})
	_, sitePub := testSigningKeys(t)
	s.TrustedKeys, s.PublicKey, s.TrustUnpinned = []string{sitePub}, "", false
	_, relPub := testSigningKeys(t)
	relFp, _ := publicFingerprint(relPub)
	if err := pinReleaseKey(&s, relPub, relFp); err == nil || !strings.Contains(err.Error(), "trustedKeys") {
		t.Fatalf("已固定 trustedKeys 的 state 不能用 add-release-key 加信任根：%v", err)
	}
	if s.PublicKey != "" || len(s.TrustedKeys) != 1 {
		t.Fatalf("拒绝后 state 不应改变：%+v", s)
	}
	// 什么都没钉定（例如 --allow-unsigned 的旧站点）仍可钉定。
	s.TrustedKeys = nil
	if err := pinReleaseKey(&s, relPub, relFp); err != nil {
		t.Fatal(err)
	}
}

func TestGpgShapeProblem(t *testing.T) {
	ok := "[GNUPG:] NEWSIG\n[GNUPG:] KEY_CONSIDERED AAAA 0\n[GNUPG:] SIG_ID x 2026-01-01 1\n[GNUPG:] GOODSIG 1234 t <t@x>\n[GNUPG:] VALIDSIG " + strings.Repeat("A", 40) + " 2026-01-01 1 0 4 0 22 8 00 " + strings.Repeat("A", 40) + "\n"
	if r := gpgShapeProblem(ok); r != "" {
		t.Fatalf("正常分离签名不应报形态问题：%s", r)
	}
	for _, bad := range []string{
		"[GNUPG:] NEWSIG\n[GNUPG:] PLAINTEXT 62 0 other\n[GNUPG:] GOODSIG 1234 t\n",
		"[GNUPG:] NEWSIG\n[GNUPG:] PLAINTEXT_LENGTH 6\n[GNUPG:] GOODSIG 1234 t\n",
		"[GNUPG:] NEWSIG\n[GNUPG:] GOODSIG 1234 t\n[GNUPG:] NEWSIG\n[GNUPG:] GOODSIG 5678 u\n",
		"[GNUPG:] GOODSIG 1234 t\n[GNUPG:] GOODSIG 5678 u\n",
	} {
		if gpgShapeProblem(bad) == "" {
			t.Fatalf("应报形态问题：%q", bad)
		}
	}
}
