package main

// 提交签名校验（设计 D3：requireSignedCommits + source.git.trustedSigners）。
//
// 只校验要部署的那个提交。信任只来自 trustedSigners 列出的公钥文件（root 所有、组和其他用户
// 不可写），不用 root 的全局 keyring、~/.ssh 或 git 配置：
//   - 提交对象用 git cat-file commit 从 root 自己 fetch 的暂存裸仓库读出（隔离的 git 环境，
//     同 withGitEnv），按 git 的规则拆出签名头 gpgsig 与被签名内容；
//   - ssh 签名（-----BEGIN SSH SIGNATURE-----）：把受信 ssh 公钥写成临时 allowed_signers
//     （固定主体 auriga-trusted、namespaces="git"），ssh-keygen -Y verify -n git 校验；
//   - gpg 签名（-----BEGIN PGP SIGNATURE-----）：受信 PGP 公钥导入临时 GNUPGHOME（0700，只含
//     这些公钥），gpg --verify 并解析 --status-fd 输出，必须有 GOODSIG 与 VALIDSIG，且不能有
//     BADSIG/ERRSIG/EXPSIG/EXPKEYSIG/REVKEYSIG；
//   - x509（gpgsm）与其它签名格式拒绝。
//
// trustedSigners 每个文件要么是 ASCII 装甲的 PGP 公钥块，要么是若干行 ssh 公钥
// （<类型> <base64> [注释]，不接受 allowed_signers 的选项写法）。

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const (
	sshSigBegin  = "-----BEGIN SSH SIGNATURE-----"
	pgpSigBegin  = "-----BEGIN PGP SIGNATURE-----"
	x509SigBegin = "-----BEGIN SIGNED MESSAGE-----"
	pgpKeyBegin  = "-----BEGIN PGP PUBLIC KEY BLOCK-----"
	// sshPrincipal 是临时 allowed_signers 里所有受信 ssh 公钥共用的主体名。
	sshPrincipal = "auriga-trusted"
)

var sshKeyTypes = map[string]bool{
	"ssh-ed25519": true, "ssh-rsa": true,
	"ecdsa-sha2-nistp256": true, "ecdsa-sha2-nistp384": true, "ecdsa-sha2-nistp521": true,
	"sk-ssh-ed25519@openssh.com": true, "sk-ecdsa-sha2-nistp256@openssh.com": true,
}

// trustedSignerSet 是从 trustedSigners 读出的受信公钥。
type trustedSignerSet struct {
	SSH []string // allowed_signers 行
	PGP [][]byte // ASCII 装甲公钥块（每个文件一份）
}

// loadTrustedSigners 读取并分类受信签名者公钥文件。
func loadTrustedSigners(paths []string) (*trustedSignerSet, error) {
	if len(paths) == 0 {
		return nil, errors.New("source.git.trustedSigners 为空：要求签名提交时必须配置受信签名者公钥")
	}
	set := &trustedSignerSet{}
	for _, path := range paths {
		if err := noSymlinks(path); err != nil {
			return nil, err
		}
		if err := depsTrustedSourceCheck("受信签名者公钥 ", path); err != nil {
			return nil, err
		}
		raw, err := smallFile(path, 1<<20)
		if err != nil {
			return nil, fmt.Errorf("读取受信签名者公钥 %s 失败：%w", path, err)
		}
		text := string(raw)
		if strings.Contains(text, pgpKeyBegin) {
			set.PGP = append(set.PGP, raw)
			continue
		}
		lines := 0
		for i, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			fields := strings.Fields(line)
			if len(fields) < 2 || !sshKeyTypes[fields[0]] {
				return nil, fmt.Errorf("受信签名者公钥 %s 第 %d 行不是 ssh 公钥（<类型> <base64> [注释]），文件也不是 PGP 公钥块", path, i+1)
			}
			blob, err := base64.StdEncoding.DecodeString(fields[1])
			if err != nil || len(blob) < 4 {
				return nil, fmt.Errorf("受信签名者公钥 %s 第 %d 行的 base64 无效", path, i+1)
			}
			set.SSH = append(set.SSH, fmt.Sprintf("%s namespaces=\"git\" %s %s", sshPrincipal, fields[0], fields[1]))
			lines++
		}
		if lines == 0 {
			return nil, fmt.Errorf("受信签名者公钥 %s 里没有公钥（需要 PGP 公钥块或 ssh 公钥行）", path)
		}
	}
	return set, nil
}

// splitCommitSignature 按 git 的规则（commit.c parse_buffer_signed_by_header，SHA-1 仓库）拆出
// 签名与被签名内容：头部区里 gpgsig 头及其续行是签名，所有 gpgsig* 头都不进被签名内容。
func splitCommitSignature(raw []byte) (payload, sig []byte, ok bool) {
	var p, sg bytes.Buffer
	inSig, other := false, false
	rest := raw
	for len(rest) > 0 {
		line := rest
		if i := bytes.IndexByte(rest, '\n'); i >= 0 {
			line = rest[:i+1]
		}
		next := rest[len(line):]
		switch {
		case inSig && len(line) > 0 && line[0] == ' ':
			sg.Write(line[1:])
		case bytes.HasPrefix(line, []byte("gpgsig ")):
			sg.Write(line[len("gpgsig "):])
			inSig, other, ok = true, false, true
		default:
			inSig = false
			if bytes.HasPrefix(line, []byte("gpgsig")) {
				other = true
			} else if other && (len(line) == 0 || line[0] != ' ') {
				other = false
			}
			if len(line) > 0 && line[0] == '\n' {
				// 头部结束：其余原样是被签名内容。
				p.Write(rest)
				next = nil
			} else if !other {
				p.Write(line)
			}
		}
		rest = next
	}
	return p.Bytes(), sg.Bytes(), ok
}

// gpgValidSig 返回 VALIDSIG 状态行里的主密钥指纹（没有主密钥字段时取签名密钥指纹）。
func gpgValidSig(status string) string {
	for _, line := range strings.Split(status, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 || fields[0] != "[GNUPG:]" || fields[1] != "VALIDSIG" {
			continue
		}
		fp := fields[2]
		if len(fields) >= 12 {
			fp = fields[11]
		}
		if fingerprintHex.MatchString(fp) {
			return fp
		}
	}
	return ""
}

// gpgShapeProblem 检查 --status-fd 输出的形态：出现 PLAINTEXT（签名里夹带了报文正文，不是
// 分离签名）或多于一个 NEWSIG/GOODSIG（多个签名）时返回原因。
func gpgShapeProblem(status string) string {
	newsig, goodsig := 0, 0
	for _, line := range strings.Split(status, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 || fields[0] != "[GNUPG:]" {
			continue
		}
		switch fields[1] {
		case "PLAINTEXT", "PLAINTEXT_LENGTH":
			return "签名里夹带报文正文，不是分离签名"
		case "NEWSIG":
			newsig++
		case "GOODSIG":
			goodsig++
		}
	}
	if newsig > 1 || goodsig > 1 {
		return "含多个签名"
	}
	return ""
}

var fingerprintHex = regexp.MustCompile(`^[0-9A-F]{40,64}$`)

// verifyCommitSignature 校验 repo（裸仓库）里 commit 的签名属于受信签名者；work 是 root 私有的
// 临时目录。返回写进收据的签名说明。
func verifyCommitSignature(ctx context.Context, repo, commit string, signers []string, work string) (string, error) {
	set, err := loadTrustedSigners(signers)
	if err != nil {
		return "", err
	}
	var raw bytes.Buffer
	if err := deployRun(ctx, withGitEnv(runSpec{Name: "git", Args: []string{"--git-dir=" + repo, "cat-file", "commit", commit}, Stdout: &raw})); err != nil {
		return "", fmt.Errorf("读取提交 %s 失败：%w", commit, err)
	}
	payload, sig, ok := splitCommitSignature(raw.Bytes())
	if !ok {
		return "", fmt.Errorf("提交 %s 没有签名，site.json 要求签名提交（requireSignedCommits=true），拒绝部署", commit)
	}
	if err := os.MkdirAll(work, 0700); err != nil {
		return "", err
	}
	dir, err := os.MkdirTemp(work, "sig-")
	if err != nil {
		return "", err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	sigFile := filepath.Join(dir, "commit.sig")
	payloadFile := filepath.Join(dir, "commit.payload")
	if err := os.WriteFile(sigFile, sig, 0600); err != nil {
		return "", err
	}
	if err := os.WriteFile(payloadFile, payload, 0600); err != nil {
		return "", err
	}
	text := strings.TrimSpace(string(sig))
	switch {
	case strings.HasPrefix(text, sshSigBegin):
		if len(set.SSH) == 0 {
			return "", fmt.Errorf("提交 %s 是 ssh 签名，但 trustedSigners 里没有 ssh 公钥：签名者不受信，拒绝部署", commit)
		}
		allowed := filepath.Join(dir, "allowed_signers")
		if err := os.WriteFile(allowed, []byte(strings.Join(set.SSH, "\n")+"\n"), 0600); err != nil {
			return "", err
		}
		f, err := os.Open(payloadFile)
		if err != nil {
			return "", err
		}
		defer func() { _ = f.Close() }()
		var out bytes.Buffer
		err = deployRun(ctx, runSpec{Name: "ssh-keygen", Args: []string{"-Y", "verify", "-f", allowed, "-I", sshPrincipal, "-n", "git", "-s", sigFile}, Stdin: f, Stdout: &out, Env: []string{"LC_ALL=C"}, Quiet: true})
		if err != nil {
			return "", fmt.Errorf("提交 %s 的 ssh 签名校验失败（签名无效，或签名者不在 trustedSigners 里）：%w", commit, err)
		}
		line := strings.TrimSpace(out.String())
		if !strings.HasPrefix(line, "Good \"git\" signature") {
			return "", fmt.Errorf("提交 %s 的 ssh 签名校验输出异常：%q", commit, line)
		}
		return "ssh：" + line, nil
	case strings.HasPrefix(text, pgpSigBegin):
		if len(set.PGP) == 0 {
			return "", fmt.Errorf("提交 %s 是 gpg 签名，但 trustedSigners 里没有 PGP 公钥：签名者不受信，拒绝部署", commit)
		}
		home := filepath.Join(dir, "gnupg")
		if err := os.Mkdir(home, 0700); err != nil {
			return "", err
		}
		env := []string{"GNUPGHOME=" + home, "LC_ALL=C"}
		base := []string{"--homedir", home, "--batch", "--no-tty", "--no-options", "--no-auto-key-retrieve", "--no-autostart"}
		for i, key := range set.PGP {
			keyFile := filepath.Join(dir, fmt.Sprintf("signer-%d.asc", i))
			if err := os.WriteFile(keyFile, key, 0600); err != nil {
				return "", err
			}
			if err := deployRun(ctx, runSpec{Name: "gpg", Args: append(append([]string(nil), base...), "--import", keyFile), Env: env, Stdout: &bytes.Buffer{}, Quiet: true}); err != nil {
				return "", fmt.Errorf("导入受信 PGP 公钥失败：%w", err)
			}
		}
		var status bytes.Buffer
		verr := deployRun(ctx, runSpec{Name: "gpg", Args: append(append([]string(nil), base...), "--status-fd", "1", "--verify", sigFile, payloadFile), Env: env, Stdout: &status, Quiet: true})
		out := status.String()
		for _, bad := range []string{"BADSIG", "ERRSIG", "EXPSIG", "EXPKEYSIG", "REVKEYSIG", "NO_PUBKEY"} {
			if strings.Contains(out, "[GNUPG:] "+bad+" ") {
				return "", fmt.Errorf("提交 %s 的 gpg 签名校验失败（%s：签名无效、密钥过期或吊销，或签名者不在 trustedSigners 里）", commit, bad)
			}
		}
		// 防御：分离签名校验不应带出报文正文（PLAINTEXT），也只应有一个签名（NEWSIG）。
		if reason := gpgShapeProblem(out); reason != "" {
			return "", fmt.Errorf("提交 %s 的 gpg 签名形态异常（%s），拒绝部署", commit, reason)
		}
		fp := gpgValidSig(out)
		if verr != nil || !strings.Contains(out, "[GNUPG:] GOODSIG ") || fp == "" {
			return "", fmt.Errorf("提交 %s 的 gpg 签名校验失败（签名者不在 trustedSigners 里或签名无效）：%v", commit, verr)
		}
		return "gpg：主密钥指纹 " + fp, nil
	case strings.HasPrefix(text, x509SigBegin):
		return "", fmt.Errorf("提交 %s 是 x509（gpgsm）签名：本版本只支持 ssh 与 gpg 签名，拒绝部署", commit)
	}
	return "", fmt.Errorf("提交 %s 的签名格式无法识别：只支持 ssh 与 gpg 签名，拒绝部署", commit)
}
