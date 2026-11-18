package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const dshPackage = "@deepseek-ai/dsh"
const dshArchivePrefix = "auriga-dsh-source-"
const dshSourcePinPath = "deploy/offline/dsh-source-pin.json"

var dshCommit = regexp.MustCompile(`^[a-f0-9]{40}$`)

type dshInstallerPin struct {
	Version   string `json:"dsh"`
	Integrity string `json:"dshIntegrity"`
}

func readDSHInstallerPin(source string) (dshInstallerPin, error) {
	var pin dshInstallerPin
	raw, err := smallFile(filepath.Join(source, "clients/installer/upstream.json"), 64<<10)
	if err != nil {
		return pin, err
	}
	if err := json.Unmarshal(raw, &pin); err != nil {
		return pin, err
	}
	sri, ok := strings.CutPrefix(pin.Integrity, "sha512-")
	sig, err := base64.StdEncoding.DecodeString(sri)
	if !regexpVersion.MatchString(pin.Version) || !ok || err != nil || len(sig) != 64 {
		return pin, errors.New("DSH 安装器版本或 npm SHA-512 无效")
	}
	return pin, nil
}

type dshArchivePin struct {
	Version string `json:"version"`
	Commit  string `json:"commit"`
	SHA256  string `json:"sha256"`
}

type dshNPMLockEntry struct {
	Version   string `json:"version"`
	Resolved  string `json:"resolved"`
	Integrity string `json:"integrity"`
}

func stageDSHSource(ctx context.Context, source, input, stage string) (string, []string, error) {
	installer, err := readDSHInstallerPin(source)
	if err != nil {
		return "", nil, err
	}
	var pin dshArchivePin
	if err := readJSON(filepath.Join(source, dshSourcePinPath), &pin); err != nil {
		return "", nil, err
	}
	if pin.Version != installer.Version || !dshCommit.MatchString(pin.Commit) || !hex64.MatchString(pin.SHA256) {
		return "", nil, errors.New("DSH 官方源码归档 pin 与安装器版本不符")
	}
	lockPath := "clients/installer/locks/dsh-" + installer.Version + ".package-lock.json"
	raw, err := smallFileContext(ctx, filepath.Join(source, lockPath), 4<<20)
	if err != nil {
		return "", nil, err
	}
	var lock struct {
		Packages map[string]dshNPMLockEntry `json:"packages"`
	}
	if err := json.Unmarshal(raw, &lock); err != nil {
		return "", nil, err
	}
	npm := lock.Packages["node_modules/"+dshPackage]
	expectedNPM := "https://registry.npmjs.org/" + dshPackage + "/-/dsh-" + pin.Version + ".tgz"
	if npm.Version != pin.Version || npm.Integrity != installer.Integrity || npm.Resolved != expectedNPM {
		return "", nil, errors.New("DSH npm 锁与安装器 pin 不一致")
	}
	root := "deepseek-harness-" + pin.Commit
	stem := dshArchivePrefix + pin.Version
	archive := filepath.Join(stage, stem+".tar.gz")
	// A local approved transfer or a Forgejo mirror preserves the exact upstream
	// bytes. The source record describes upstream, without exposing private mirror URLs.
	if filepath.IsAbs(input) {
		if err := copyFileContext(ctx, input, archive, 0644); err != nil {
			return "", nil, err
		}
		hash, err := digestContext(ctx, archive)
		if err != nil {
			return "", nil, err
		}
		if hash != pin.SHA256 {
			return "", nil, errors.New("DSH 源码归档 SHA256 与 pin 不符")
		}
	} else {
		u, err := url.Parse(input)
		if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
			return "", nil, errors.New("DSH 源码输入需绝对本地路径或无凭据的 Forgejo HTTPS URL")
		}
		if err := fetchContext(ctx, artifact{URL: input, SHA256: pin.SHA256}, archive); err != nil {
			return "", nil, err
		}
	}
	license, err := inspectDSHArchive(ctx, archive, root, pin.Version)
	if err != nil {
		return "", nil, err
	}
	licenseFile := filepath.Join(stage, stem+"-LICENSE.txt")
	if err := os.WriteFile(licenseFile, license, 0644); err != nil {
		return "", nil, err
	}
	archiveInfo, err := describeAssetContext(ctx, archive)
	if err != nil {
		return "", nil, err
	}
	licenseInfo, err := describeAssetContext(ctx, licenseFile)
	if err != nil {
		return "", nil, err
	}
	record := struct {
		Schema      int             `json:"schemaVersion"`
		Package     string          `json:"package"`
		Repository  string          `json:"repository"`
		Tag         string          `json:"tag"`
		Commit      string          `json:"commit"`
		UpstreamURL string          `json:"upstreamArchiveUrl"`
		PackagePath string          `json:"upstreamPackagePath"`
		NPMLockPath string          `json:"npmLockPath"`
		NPMLockSHA  string          `json:"npmLockSha256"`
		NPM         dshNPMLockEntry `json:"npmRelease"`
		Archive     assetInfo       `json:"archive"`
		License     assetInfo       `json:"license"`
		Relation    string          `json:"sourceRelation"`
	}{
		Schema: 1, Package: dshPackage, Repository: "https://github.com/deepseek-ai/deepseek-harness",
		Tag: "dsh-v" + pin.Version, Commit: pin.Commit,
		UpstreamURL: "https://codeload.github.com/deepseek-ai/deepseek-harness/tar.gz/" + pin.Commit,
		PackagePath: "apps/cli/package.json", NPMLockPath: lockPath, NPMLockSHA: fmt.Sprintf("%x", sha256.Sum256(raw)), NPM: npm, Archive: archiveInfo, License: licenseInfo,
		Relation: "官方标签提交内的包名和版本与 npm 锁一致。npm 发布 tarball 的完整性由锁记录，上游未提供 npm gitHead。",
	}
	recordFile := filepath.Join(stage, stem+"-provenance.json")
	if err := jsonFile(recordFile, record, 0644); err != nil {
		return "", nil, err
	}
	return archive, []string{licenseFile, recordFile}, nil
}

func inspectDSHArchive(ctx context.Context, archive, root, version string) ([]byte, error) {
	in, err := os.Open(archive)
	if err != nil {
		return nil, err
	}
	defer func() { _ = in.Close() }()
	compressed, err := gzip.NewReader(cancellationReader(ctx, in))
	if err != nil {
		return nil, err
	}
	defer func() { _ = compressed.Close() }()
	tarReader := tar.NewReader(compressed)
	files := map[string][]byte{root + "/LICENSE": nil, root + "/apps/cli/package.json": nil}
	for {
		h, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if old, wanted := files[h.Name]; wanted {
			if old != nil || h.Typeflag != tar.TypeReg || h.Size <= 0 || h.Size > 1<<20 {
				return nil, errors.New("DSH 归档内的 LICENSE 或包元数据无效")
			}
			if files[h.Name], err = io.ReadAll(tarReader); err != nil {
				return nil, err
			}
		}
	}
	var pkg struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	}
	license := files[root+"/LICENSE"]
	if err := json.Unmarshal(files[root+"/apps/cli/package.json"], &pkg); err != nil {
		return nil, err
	}
	if pkg.Name != dshPackage || pkg.Version != version || !bytes.Contains(license, []byte("MIT License")) {
		return nil, fmt.Errorf("DSH 上游包须匹配 %s@%s 且包含 MIT LICENSE", dshPackage, version)
	}
	return license, nil
}
