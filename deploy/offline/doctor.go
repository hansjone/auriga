package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func waitHealthy(s state) error {
	return waitHealthyFor(s, 5*time.Minute)
}

// waitHealthyFor 等待全部服务健康，超时返回最后一次的不健康原因（deploy 用 server.healthTimeout，默认 10 分钟）。
func waitHealthyFor(s state, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		err := health(s)
		if err == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("服务健康等待超过 %s（%v），运行 ./auriga doctor 检查", timeout, err)
		}
		time.Sleep(2 * time.Second)
	}
}

func health(s state) error {
	var out bytes.Buffer
	if err := compose(s, &out, "ps", "--all", "--format", "json"); err != nil {
		return err
	}
	return healthyContainers(out.Bytes())
}

func healthyContainers(raw []byte) error {
	type container struct {
		Service, State, Health string
		ExitCode               int
	}
	var records []container
	if bytes.HasPrefix(bytes.TrimSpace(raw), []byte("[")) {
		if err := json.Unmarshal(raw, &records); err != nil {
			return err
		}
	} else {
		decoder := json.NewDecoder(bytes.NewReader(raw))
		for {
			var c container
			err := decoder.Decode(&c)
			if err == io.EOF {
				break
			}
			if err != nil {
				return err
			}
			records = append(records, c)
		}
	}
	seen := map[string]bool{}
	for _, c := range records {
		seen[c.Service] = true
		if strings.HasSuffix(c.Service, "-prepare") && c.State == "exited" && c.ExitCode == 0 {
			continue
		}
		if c.State != "running" || c.Health != "" && c.Health != "healthy" {
			return fmt.Errorf("服务 %s state=%s health=%s", c.Service, c.State, c.Health)
		}
	}
	for _, name := range []string{"postgres", "nats", "registry", "platform", "edge", "agent", "bifrost", "egress"} {
		if !seen[name] {
			return fmt.Errorf("缺少服务 %s", name)
		}
	}
	return nil
}

func doctor(s state) error {
	var installed release
	if err := readJSON(filepath.Join(s.Package, "offline-release.json"), &installed); err == nil {
		fmt.Println(printProvenance(installed.Source))
		if line := siteDependenciesInfo(s.Package); line != "" {
			fmt.Println(line)
		}
	}
	failures := 0
	check := func(name, repair string, test func() error) {
		if err := test(); err != nil {
			failures++
			fmt.Printf("FAIL %s — %v\n  修复建议 %s\n", name, err, repair)
		} else {
			fmt.Printf("PASS %s\n", name)
		}
	}
	check("Docker/Compose版本", "离线预装 Docker Engine 与 Compose 2.29+，检查本机 socket。", dockerReady)
	check("发行信任状态", "从批准公钥重新建立受信安装；无签名历史标记不会自动清除。", func() error {
		return trustHealth(s)
	})
	check("离线包完整性", "从受信原包恢复发行文件。", func() error {
		if err := verifySums(s.Package); err != nil {
			return err
		}
		_, err := verifyStatePackage(s, s.Package, "", false)
		return err
	})
	check("镜像摘要与层", "重拷离线包并由 install/upgrade 重新 load，勿直接替换镜像。", func() error {
		var r release
		if err := readJSON(filepath.Join(s.Package, "offline-release.json"), &r); err != nil {
			return err
		}
		for name, id := range s.Images {
			platform, err := capture("docker", "image", "inspect", "--format", "{{.Os}}/{{.Architecture}}", id)
			if err != nil || platform != "linux/amd64" {
				return fmt.Errorf("%s 镜像缺失或架构错误", name)
			}
		}
		return checkLoadedImages(s, r)
	})
	check("配置及原有安全门禁", "按诊断修复原配置，保留密钥与材料回执。", func() error { return nas(s, "preflight") })
	check("资源私钥权限", "从原备份恢复 UID10001、0700目录、0600私钥。", func() error {
		root := filepath.Join(s.Data, "resource-keys/platform")
		for path, want := range map[string]string{root: "10001:10001:700", filepath.Join(root, "release.pk8"): "10001:10001:600", filepath.Join(root, "revocation.pk8"): "10001:10001:600"} {
			if err := noSymlinks(path); err != nil {
				return err
			}
			got, err := capture("stat", "-c", "%u:%g:%a", path)
			if err != nil {
				return err
			}
			if got != want {
				return errors.New("资源私钥属主或权限不符")
			}
		}
		return nil
	})
	check("数据库", "检查 postgres 状态和磁盘，保留数据库及密钥，恢复前先备份。", func() error {
		return compose(s, nil, "exec", "-T", "postgres", "pg_isready", "-U", "auriga_bootstrap", "-d", "auriga")
	})
	check("各服务健康", "检查 ./auriga logs，修复失败服务。", func() error { return health(s) })
	check("正常管理台入口", "检查现场地址、DNS或受信代理及端口映射。", func() error { return browserEntry(s) })
	check("对象存储UID10001写入", "恢复资源对象目录属主10001及0700权限，检查磁盘。", func() error { return objectWrite(s) })
	check("真实登录及资源撤销签名", "私密TTY查看应急材料并核对时间与账户，使用正常页面登录。代理模式核对mTLS接线。", func() error { return loginProbe(s) })
	var r release
	if err := readJSON(filepath.Join(s.Package, "offline-release.json"), &r); err == nil && r.Desktop == nil {
		fmt.Println("SKIP 桌面发行 — 未随包附带，桌面端验收待单独执行")
	} else {
		check("桌面发行", "将公钥根传给桌面构建 --resource-trust-json，导入已签名桌面发行。", func() error {
			return errors.New("桌面包待正常管理台导入及真实客户端安装验收")
		})
	}
	fmt.Printf("doctor 结束，失败项 %d。输出不含凭据。\n", failures)
	if failures > 0 {
		return errors.New("请按 FAIL 修复建议处理")
	}
	return nil
}

func browserEntry(s state) error {
	var access struct {
		AdminOrigin struct {
			Scheme, Host string
			Port         int
		}
	}
	if err := readJSONLoose(s.Access, &access); err != nil {
		return err
	}
	u := fmt.Sprintf("%s://%s:%d/", access.AdminOrigin.Scheme, access.AdminOrigin.Host, access.AdminOrigin.Port)
	client := &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) > 3 {
			return errors.New("重定向过多")
		}
		return nil
	}}
	r, err := client.Get(u)
	if err != nil {
		return errors.New("管理台地址不可达或TLS校验失败")
	}
	defer func() { _ = r.Body.Close() }()
	if r.StatusCode != 200 || !strings.Contains(r.Header.Get("Content-Type"), "text/html") {
		return fmt.Errorf("管理台未返回HTML，HTTP %d", r.StatusCode)
	}
	return nil
}

func readJSONLoose(path string, value any) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, value)
}

func objectWrite(s state) error {
	return command(nil, io.Discard, nil, "docker", "run", "--rm", "--pull", "never", "--network", "none", "--log-driver", "none", "--user", "10001:10001", "--read-only", "--cap-drop", "ALL", "--security-opt", "no-new-privileges:true", "--mount", "type=bind,src="+filepath.Join(s.Data, "resource-objects")+",dst=/objects", "--entrypoint", "sh", s.Images["POSTGRES"], "-ec", "umask 077; f=$(mktemp /objects/.doctor.XXXXXXXX); trap 'rm -f \"$f\"' EXIT; printf test >\"$f\"; test -s \"$f\"")
}

func checkLoadedImages(s state, r release) error {
	boot := filepath.Join(s.Package, "deploy-nas/bin/auriga-nas-bootstrap")
	for _, name := range []string{"platform", "edge", "agent", "bifrost", "operator"} {
		id, err := captureImageResolve(context.Background(), boot, filepath.Join(s.Package, "images", name+".tar"), "auriga/"+name+":nas", s.Images[strings.ToUpper(name)])
		if err != nil {
			return err
		}
		if s.Images[strings.ToUpper(name)] != id {
			return errors.New("当前服务镜像与安装时身份不符")
		}
	}
	for name, entry := range r.Images {
		id, err := captureImageResolve(context.Background(), boot, filepath.Join(s.Package, entry.Archive), entry.Tag, s.Images[strings.ToUpper(strings.ReplaceAll(name, "-", "_"))])
		if err != nil {
			return err
		}
		if s.Images[strings.ToUpper(strings.ReplaceAll(name, "-", "_"))] != id {
			return errors.New("附加镜像身份发生变化")
		}
	}
	return nil
}

func loginProbe(s state) error {
	edge, err := capture("docker", "ps", "-q", "--no-trunc", "--filter", "label=com.docker.compose.project=auriga-nas-test", "--filter", "label=com.docker.compose.service=edge")
	if err != nil || !hex64.MatchString(edge) {
		return errors.New("找不到唯一运行的edge")
	}
	args := []string{"run", "--rm", "--pull", "never", "--log-driver", "none", "--network", "container:" + edge, "--user", "10001:10001", "--cap-drop", "ALL", "--read-only", "--security-opt", "no-new-privileges:true", "--env", "PYTHONDONTWRITEBYTECODE=1"}
	for source, target := range map[string]string{filepath.Join(s.Package, "deploy-nas/scripts"): "/tools", s.Access: "/access.json", filepath.Join(s.Data, "enrollment/emergency-enrollment.json"): "/enrollment.json", filepath.Join(s.Data, "resource-keys/resource-roots.json"): "/roots.json"} {
		args = append(args, "--mount", "type=bind,src="+source+",dst="+target+",readonly")
	}
	args = append(args, "--entrypoint", "python", s.Images["OPERATOR"], "/tools/oneclick-doctor.py")
	return command(nil, os.Stdout, nil, "docker", args...)
}
