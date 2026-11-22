package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type trustOptions struct {
	Key           string
	Fingerprint   string
	AllowUnsigned bool
}

func sourceVerificationKey(source string) (string, error) {
	if _, err := os.Lstat(filepath.Join(source, "auriga-source.json")); os.IsNotExist(err) {
		return "", nil
	}
	if _, err := readSourceManifest(source); err != nil {
		return "", err
	}
	return filepath.Join(source, "release-public.pem"), nil
}

func (o *trustOptions) flags(f *flag.FlagSet) {
	f.StringVar(&o.Key, "verify-key", "", "")
	f.StringVar(&o.Fingerprint, "trust-fingerprint", "", "")
	f.BoolVar(&o.AllowUnsigned, "allow-unsigned", false, "")
}

// Fingerprints are SHA256 over canonical SPKI DER, independent of PEM wrapping.
func publicFingerprint(file string) (string, error) {
	raw, err := smallFile(file, 64<<10)
	if err != nil {
		return "", err
	}
	block, rest := pem.Decode(raw)
	if block == nil || len(strings.TrimSpace(string(rest))) != 0 {
		return "", errors.New("需要单个SPKI PEM公钥")
	}
	key, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return "", err
	}
	if _, ok := key.(ed25519.PublicKey); !ok {
		return "", errors.New("签名公钥需要Ed25519")
	}
	der, err := x509.MarshalPKIXPublicKey(key)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(der)
	return hex.EncodeToString(sum[:]), nil
}

func unsignedWarning() {
	fmt.Fprintln(os.Stderr, "警告：--allow-unsigned 已显式允许未签名材料，发行身份未核验；安装状态将持续报警。")
}

func trustHealth(s state) error {
	if s.AllowUnsigned {
		return errors.New("警告：state记录曾使用--allow-unsigned，发行身份未核验")
	}
	// 本机构建的发行不靠发行钥（信任依据是 root 自取的源码与 releaseSums）；未钉定只影响之后的
	// 签名包 upgrade，由 doctor 另行提示。
	if s.source() == sourceLocalDeploy && len(s.TrustedKeys) == 0 && s.PublicKey == "" {
		return nil
	}
	_, err := s.trustKeys()
	return err
}

func missingSignature(root, key string) bool {
	if _, err := os.Stat(filepath.Join(root, "SHA256SUMS.sig")); os.IsNotExist(err) {
		return true
	}
	if key == "" {
		key = filepath.Join(root, "release-public.pem")
	}
	_, err := os.Stat(key)
	return os.IsNotExist(err)
}

// Used only at operator-facing first-trust boundaries. Internal metadata readers
// still require signatures; an unsigned exception does not persist as trust.
func verifyRootTrust(root string, o trustOptions) (string, bool, error) {
	key := o.Key
	if key == "" {
		key = filepath.Join(root, "release-public.pem")
	}
	if o.Fingerprint != "" && !hex64.MatchString(o.Fingerprint) {
		return "", false, errors.New("公钥指纹需要64位小写SHA256（SPKI DER）")
	}
	fp, fpErr := publicFingerprint(key)
	if fpErr != nil {
		if !os.IsNotExist(fpErr) || o.Key != "" || o.Fingerprint != "" {
			return "", false, fpErr
		}
	} else {
		fmt.Printf("签名公钥指纹 SHA256(SPKI) %s\n", fp)
		if o.Fingerprint != "" && fp != o.Fingerprint {
			return "", false, errors.New("发行公钥指纹不匹配")
		}
	}
	if missingSignature(root, o.Key) {
		if o.AllowUnsigned {
			unsignedWarning()
			return "", true, nil
		}
		return "", false, errors.New("缺少签名或公钥；默认拒绝，仅可显式--allow-unsigned并接受持续报警")
	}
	if err := verifySignature(root, key); err != nil {
		return "", false, err
	}
	if o.Key == "" && o.Fingerprint == "" {
		return "", false, fmt.Errorf("仅完整性，未核验发行身份；请从独立渠道核对公钥指纹后使用 --trust-fingerprint %s 重跑", fp)
	}
	return key, false, nil
}

func (s state) trustKeys() ([]string, error) {
	keys := append([]string(nil), s.TrustedKeys...)
	if len(keys) == 0 && s.PublicKey != "" {
		keys = append(keys, s.PublicKey)
	}
	if len(keys) == 0 {
		return nil, errors.New("state未固定任何发行/站点公钥；不能使用包内公钥替换信任")
	}
	for _, key := range keys {
		if err := noSymlinks(key); err != nil {
			return nil, err
		}
		if _, err := publicFingerprint(key); err != nil {
			return nil, err
		}
	}
	return keys, nil
}

// verifyStatePackage 核验发行目录或待升级的签名包。对当前发行（root 即 s.Package）：
// local-deploy 来源核对本地清单 SHA256SUMS 与 state 锚定的 releaseSums（不要求签名，也不借用
// AllowUnsigned，设计 D10）；签名包来源按 state 固定的公钥验签，有历史记录时同样核对
// releaseSums。待升级的包（root 不是当前发行）一律按 state 固定的公钥验签。
func verifyStatePackage(s state, root, index string, allow bool) (bool, error) {
	current := filepath.Clean(root) == filepath.Clean(s.Package)
	var rec *deployRecord
	if current {
		if rec = s.record(s.Current); rec != nil && filepath.Clean(rec.Release) != filepath.Clean(s.Package) {
			return false, fmt.Errorf("state 的当前发行 r%d 与安装目录不一致", rec.Seq)
		}
	}
	if current && s.source() == sourceLocalDeploy {
		if rec == nil {
			return false, fmt.Errorf("state 缺少当前发行 r%d 的历史记录，无法核对本地清单", s.Current)
		}
		return false, checkReleaseDir(s.Data, rec)
	}
	if rec != nil {
		if err := checkReleaseDir(s.Data, rec); err != nil {
			return false, err
		}
	}
	// Only missing signatures may use the explicit emergency exception; a bad
	// signature or an unknown signer can never fall back to package-contained keys.
	if _, err := os.Stat(filepath.Join(root, "SHA256SUMS.sig")); os.IsNotExist(err) && allow {
		unsignedWarning()
		return true, nil
	}
	keys, err := s.trustKeys()
	if err != nil {
		return false, err
	}
	for _, key := range keys {
		if err := verifySignature(root, key); err != nil {
			continue
		}
		if index != "" {
			if err := verifyFileSignature(index, key); err != nil {
				return false, err
			}
		}
		return false, nil
	}
	return false, errors.New("签名不属于state固定的发行/已登记站点公钥，拒绝包内公钥替换")
}

func pinInitialTrust(s *state, key string) error {
	fp, err := publicFingerprint(key)
	if err != nil {
		return err
	}
	target := filepath.Join(s.Data+".oneclick", "trusted-keys", fp+".pem")
	if err := copyTrustKey(key, target, fp); err != nil {
		return err
	}
	s.PublicKey = target
	s.TrustedKeys = []string{target}
	return nil
}

func copyTrustKey(source, target, fingerprint string) error {
	if err := noSymlinks(target); err != nil {
		return err
	}
	if _, err := os.Lstat(target); !os.IsNotExist(err) {
		actual, err := publicFingerprint(target)
		if err != nil || actual != fingerprint {
			return errors.New("已有公钥文件不匹配，拒绝覆盖")
		}
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
		return err
	}
	stage, err := os.MkdirTemp(filepath.Dir(target), ".key-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(stage) }()
	temp := filepath.Join(stage, "public.pem")
	if err := copyFile(source, temp, 0644); err != nil {
		return err
	}
	actual, err := publicFingerprint(temp)
	if err != nil || actual != fingerprint {
		return errors.New("复制期间公钥发生变化")
	}
	return os.Rename(temp, target)
}

func generateSiteKey(target string) error {
	if _, err := os.Lstat(target); !os.IsNotExist(err) {
		return errors.New("站点密钥目录已存在或不可访问，拒绝覆盖")
	}
	if err := noSymlinks(filepath.Dir(target)); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
		return err
	}
	stage, err := os.MkdirTemp(filepath.Dir(target), ".auriga-site-key-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(stage) }()
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	der, err := x509.MarshalPKCS8PrivateKey(private)
	if err != nil {
		return err
	}
	secret := filepath.Join(stage, "site-private.pem")
	if err := os.WriteFile(secret, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0600); err != nil {
		return err
	}
	if err := signingPublic(secret, filepath.Join(stage, "site-public.pem")); err != nil {
		return err
	}
	return os.Rename(stage, target)
}

func keygenCommand(args []string) error {
	if len(args) == 0 || args[0] != "site" {
		return errors.New("使用 keygen site --out <全新私密目录>")
	}
	f := flags("keygen site")
	out := f.String("out", "site-build-key", "")
	if err := f.Parse(args[1:]); err != nil {
		return err
	}
	if f.NArg() != 0 {
		return errors.New("多余keygen参数")
	}
	target, err := filepath.Abs(*out)
	if err != nil {
		return err
	}
	if err := generateSiteKey(target); err != nil {
		return err
	}
	fp, err := publicFingerprint(filepath.Join(target, "site-public.pem"))
	if err != nil {
		return err
	}
	fmt.Printf("站点构建密钥已生成 %s\n公钥指纹 SHA256(SPKI) %s\n只拷公钥到服务器 trust add-site-key 登记，私钥留在受控开发机。\n", target, fp)
	return nil
}

func registerSiteKey(s *state, key, confirm string) error {
	fp, err := publicFingerprint(key)
	if err != nil {
		return err
	}
	if !hex64.MatchString(confirm) || fp != confirm {
		return errors.New("站点公钥与--confirm-fingerprint不符，拒绝登记")
	}
	keys, err := s.trustKeys()
	if err != nil {
		// 首装没有钉定发行钥的站点（trustUnpinned）可以直接登记站点钥作为第一把受信公钥。
		if !s.TrustUnpinned || len(s.TrustedKeys) != 0 || s.PublicKey != "" {
			return err
		}
		keys = nil
	}
	s.TrustedKeys = keys
	target := filepath.Join(s.Data+".oneclick", "trusted-keys", fp+".pem")
	for _, existing := range s.TrustedKeys {
		if existing == target {
			return nil
		}
	}
	if err := copyTrustKey(key, target, fp); err != nil {
		return err
	}
	s.TrustedKeys = append(s.TrustedKeys, target)
	return nil
}

// pinReleaseKey：首装未钉定发行钥的站点（deploy 首装没有带 trustFingerprint 的套件）登记发行
// 公钥；已钉定的站点不能替换。保留已登记的站点钥，清除 trustUnpinned。
func pinReleaseKey(s *state, key, confirm string) error {
	if s.PublicKey != "" {
		return errors.New("state 已钉定发行公钥，不能替换（轮换发行钥不在本工具范围）")
	}
	// 只用于首装未钉定的站点：已靠 trustedKeys 钉定信任（未标记 trustUnpinned）的 state 不能借此
	// 再加一个信任根，站点钥请用 add-site-key / remove-site-key。
	if !s.TrustUnpinned && len(s.TrustedKeys) != 0 {
		return errors.New("state 已固定受信公钥（trustedKeys），add-release-key 只用于首装未钉定发行钥的站点")
	}
	fp, err := publicFingerprint(key)
	if err != nil {
		return err
	}
	if !hex64.MatchString(confirm) || fp != confirm {
		return errors.New("发行公钥与--confirm-fingerprint不符，拒绝钉定")
	}
	target := filepath.Join(s.Data+".oneclick", "trusted-keys", fp+".pem")
	if err := copyTrustKey(key, target, fp); err != nil {
		return err
	}
	keys := []string{target}
	for _, existing := range s.TrustedKeys {
		if existing != target {
			keys = append(keys, existing)
		}
	}
	s.PublicKey, s.TrustedKeys, s.TrustUnpinned = target, keys, false
	return nil
}

func trustCommand(args []string) error {
	if len(args) < 2 || args[0] != "add-site-key" && args[0] != "remove-site-key" && args[0] != "add-release-key" {
		return errors.New("trust add-release-key <pem> --confirm-fingerprint <sha>、add-site-key <pem> --confirm-fingerprint <sha> 或 remove-site-key <sha>")
	}
	f := flags("trust")
	data := f.String("data", "/srv/auriga", "")
	confirm := f.String("confirm-fingerprint", "", "")
	if err := f.Parse(args[2:]); err != nil {
		return err
	}
	if f.NArg() != 0 {
		return errors.New("多余trust参数")
	}
	root, err := dataPath(*data)
	if err != nil {
		return err
	}
	return withLock(root, "trust", func() error {
		s, err := readState(root)
		if err != nil {
			return err
		}
		if args[0] == "add-site-key" {
			if err := registerSiteKey(&s, args[1], *confirm); err != nil {
				return err
			}
			return saveState(s)
		}
		if args[0] == "add-release-key" {
			if err := pinReleaseKey(&s, args[1], *confirm); err != nil {
				return err
			}
			return saveState(s)
		}
		if !hex64.MatchString(args[1]) {
			return errors.New("需要64位站点公钥指纹")
		}
		var keep []string
		found := false
		for _, key := range s.TrustedKeys {
			fp, err := publicFingerprint(key)
			if err != nil {
				return err
			}
			if fp == args[1] {
				if key == s.PublicKey {
					return errors.New("不能移除固定发行公钥")
				}
				found = true
				continue
			}
			keep = append(keep, key)
		}
		if !found {
			return errors.New("未登记该站点公钥")
		}
		s.TrustedKeys = keep
		if _, err := verifyStatePackage(s, s.Package, "", false); err != nil {
			return errors.New("当前安装包仍依赖该站点钥；先升级到新钥签名的版本再移除")
		}
		return saveState(s)
	})
}
