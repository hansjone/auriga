// Package runtimeimport transports a verified OCI layout to one approved local registry.
package runtimeimport

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/hansjone/auriga/infra/agent/internal/releaseverify"
	"github.com/hansjone/auriga/infra/agent/runtimebundle"
)

const operationTimeout = 5 * time.Minute

var repoPart = regexp.MustCompile(`^[a-z0-9]+(?:[._-][a-z0-9]+)*$`)

// Options requires an independently approved exact image and executable.
type Options struct {
	Stage runtimebundle.StageLayoutOptions
	ORAS  string
}

// Import first completes StageLayout, then copies by its signed digest and
// reads every object back from the exact local HTTP repository.
func Import(ctx context.Context, o Options) (runtimebundle.LayoutSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return runtimebundle.LayoutSnapshot{}, err
	}
	if runtime.GOOS != "linux" {
		return runtimebundle.LayoutSnapshot{}, errors.New("import-layout requires Linux")
	}
	address, repo, digest, err := localImage(o.Stage.Verify.Policy.AllowedImage)
	if err != nil {
		return runtimebundle.LayoutSnapshot{}, err
	}
	if !filepath.IsAbs(o.ORAS) {
		return runtimebundle.LayoutSnapshot{}, errors.New("--oras must be an absolute executable path")
	}
	// This is a compatibility check, not approval of the binary's provenance.
	st, err := os.Stat(o.ORAS)
	if err != nil || !st.Mode().IsRegular() || st.Mode()&0o111 == 0 {
		return runtimebundle.LayoutSnapshot{}, errors.New("--oras is not a regular executable")
	}
	snapshot, err := runtimebundle.StageLayout(o.Stage)
	if err != nil {
		return runtimebundle.LayoutSnapshot{}, err
	}
	if err := ctx.Err(); err != nil {
		return runtimebundle.LayoutSnapshot{}, err
	}
	if snapshot.ManifestDigest != digest {
		return runtimebundle.LayoutSnapshot{}, errors.New("staged manifest differs from approved image")
	}
	material, err := runtimebundle.Verify(o.Stage.Verify)
	if err != nil {
		return runtimebundle.LayoutSnapshot{}, err
	}
	manifest, config, layers := material.Descriptors()
	if err := checkSnapshot(snapshot, manifest, config, layers); err != nil {
		return runtimebundle.LayoutSnapshot{}, err
	}
	if err := ctx.Err(); err != nil {
		return runtimebundle.LayoutSnapshot{}, err
	}
	// StageLayout has its own explicit byte budget; the clock bounds the
	// external copy and complete HTTP readback that follow it.
	operationCtx, cancel := context.WithTimeout(ctx, operationTimeout)
	defer cancel()
	// Both registry endpoints get the same isolated, empty auth file. The
	// temporary directory is ours, owner-only, and never reused as a login home.
	authDir, err := os.MkdirTemp("", "auriga-oras-auth-")
	if err != nil {
		return runtimebundle.LayoutSnapshot{}, errors.New("cannot create isolated auth directory")
	}
	defer func() {
		_ = os.Remove(filepath.Join(authDir, "config.json"))
		_ = os.Remove(authDir)
	}()
	if err := os.Chmod(authDir, 0o700); err != nil {
		return runtimebundle.LayoutSnapshot{}, errors.New("cannot protect isolated auth directory")
	}
	authFile := filepath.Join(authDir, "config.json")
	if err := os.WriteFile(authFile, []byte("{}\n"), 0o600); err != nil {
		return runtimebundle.LayoutSnapshot{}, errors.New("cannot create empty auth config")
	}
	if err := operationCtx.Err(); err != nil {
		return runtimebundle.LayoutSnapshot{}, err
	}
	env := []string{"HOME=" + authDir, "XDG_CONFIG_HOME=" + authDir, "DOCKER_CONFIG=" + authDir, "NO_PROXY=127.0.0.1", "no_proxy=127.0.0.1"}
	version, err := runORAS(operationCtx, o.ORAS, env, "version")
	if err != nil || !version130(version) {
		return runtimebundle.LayoutSnapshot{}, errors.New("ORAS 1.3.0 compatibility check failed")
	}
	// The tag is only a transport handle. No index annotation selects the source.
	tag := "auriga-import-" + strings.TrimPrefix(digest, "sha256:")
	stagedPath, err := filepath.Abs(o.Stage.OutputDir)
	if err != nil {
		return runtimebundle.LayoutSnapshot{}, errors.New("cannot resolve staged layout path")
	}
	source := stagedPath + "@" + digest
	barrier, err := newTransportBarrier(operationCtx, address, repo, tag, snapshot, manifest, config, layers)
	if err != nil {
		return runtimebundle.LayoutSnapshot{}, err
	}
	defer barrier.Close()
	target := barrier.Address() + "/" + repo + ":" + tag
	_, err = runORAS(operationCtx, o.ORAS, env, "cp", "--from-oci-layout", "--to-plain-http", "--from-registry-config", authFile, "--to-registry-config", authFile, "--no-tty", source, target)
	barrier.Close()
	if err != nil {
		return runtimebundle.LayoutSnapshot{}, err
	}
	if barrier.Blocked() {
		return runtimebundle.LayoutSnapshot{}, errors.New("registry transport response crossed the approved boundary")
	}
	if err := readBack(operationCtx, address, repo, material, manifest, config, layers); err != nil {
		return runtimebundle.LayoutSnapshot{}, err
	}
	return snapshot, nil
}

func version130(out []byte) bool {
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "Version:") && strings.TrimSpace(strings.TrimPrefix(line, "Version:")) == "1.3.0" {
			return true
		}
	}
	return false
}

func localImage(image string) (address, repo, digest string, err error) {
	parts := strings.Split(image, "@")
	if len(parts) != 2 || !strings.HasPrefix(parts[0], "127.0.0.1:") || len(parts[1]) != 71 || !strings.HasPrefix(parts[1], "sha256:") {
		return "", "", "", errors.New("approved image must be a canonical numeric loopback repo@sha256")
	}
	for _, c := range parts[1][7:] {
		if c < '0' || c > '9' && (c < 'a' || c > 'f') {
			return "", "", "", errors.New("approved image digest is not lowercase hex")
		}
	}
	locator := strings.TrimPrefix(parts[0], "127.0.0.1:")
	slash := strings.IndexByte(locator, '/')
	if slash < 1 {
		return "", "", "", errors.New("approved image has no repository")
	}
	port, path := locator[:slash], locator[slash+1:]
	if port[0] == '0' || len(port) > 5 {
		return "", "", "", errors.New("noncanonical local registry port")
	}
	for _, c := range port {
		if c < '0' || c > '9' {
			return "", "", "", errors.New("noncanonical local registry port")
		}
	}
	p, parseErr := strconv.Atoi(port)
	if parseErr != nil || p < 1 || p > 65535 {
		return "", "", "", errors.New("invalid local registry port")
	}
	for _, part := range strings.Split(path, "/") {
		if !repoPart.MatchString(part) {
			return "", "", "", errors.New("noncanonical repository")
		}
	}
	return "127.0.0.1:" + port, path, parts[1], nil
}

func checkSnapshot(s runtimebundle.LayoutSnapshot, m, c releaseverify.BlobDescriptor, layers []releaseverify.BlobDescriptor) error {
	if s.ManifestDigest != m.Digest || s.ConfigDigest != c.Digest {
		return errors.New("staged descriptor mismatch")
	}
	seen := map[string]releaseverify.BlobDescriptor{}
	var total uint64
	for _, d := range append([]releaseverify.BlobDescriptor{m, c}, layers...) {
		if old, ok := seen[d.Digest]; ok {
			if old != d {
				return errors.New("conflicting verified descriptor")
			}
			continue
		}
		seen[d.Digest] = d
		total += d.Size
	}
	if len(seen) != s.BlobCount || total != uint64(s.TotalBytes) {
		return errors.New("staged byte count mismatch")
	}
	return nil
}

func runORAS(ctx context.Context, path string, env []string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Env = env
	setProcessGroup(cmd)
	cmd.WaitDelay = time.Second
	out := &boundedOutput{limit: 4096}
	cmd.Stdout, cmd.Stderr = out, out
	err := cmd.Run()
	finishProcessGroup(cmd)
	if err != nil || ctx.Err() != nil {
		return nil, errors.New("ORAS failed or timed out")
	}
	if out.exceeded {
		return nil, errors.New("ORAS output exceeded limit")
	}
	return out.buf.Bytes(), nil
}

type boundedOutput struct {
	buf      bytes.Buffer
	limit    int
	exceeded bool
}

func (b *boundedOutput) Write(p []byte) (int, error) {
	n := len(p)
	remaining := b.limit - b.buf.Len()
	if remaining > 0 {
		_, _ = b.buf.Write(p[:min(n, remaining)])
	}
	if n > remaining {
		b.exceeded = true
	}
	return n, nil
}

func readBack(ctx context.Context, address, repo string, material *runtimebundle.Material, m, c releaseverify.BlobDescriptor, layers []releaseverify.BlobDescriptor) error {
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	transport := &http.Transport{Proxy: nil, DisableCompression: true, DisableKeepAlives: true, MaxResponseHeaderBytes: 16 << 10,
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return dialer.DialContext(ctx, "tcp", address)
		}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	seen := map[string]bool{}
	for i, d := range append([]releaseverify.BlobDescriptor{m, c}, layers...) {
		if seen[d.Digest] {
			continue
		}
		seen[d.Digest] = true
		kind := "blobs"
		var original []byte
		switch i {
		case 0:
			kind, original = "manifests", material.Manifest()
		case 1:
			original = material.Config()
		}
		if err := getBlob(ctx, client, address, repo, kind, d, original); err != nil {
			return err
		}
	}
	return nil
}

func getBlob(ctx context.Context, client *http.Client, address, repo, kind string, d releaseverify.BlobDescriptor, original []byte) error {
	u := &url.URL{Scheme: "http", Host: address, Path: "/v2/" + repo + "/" + kind + "/" + d.Digest}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return errors.New("cannot form local registry read")
	}
	req.Header.Set("Accept-Encoding", "identity")
	req.Header.Set("Accept", d.MediaType)
	res, err := client.Do(req)
	if err != nil {
		return errors.New("local registry read failed")
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK || res.Header.Get("Content-Encoding") != "" || res.ContentLength >= 0 && uint64(res.ContentLength) != d.Size {
		return errors.New("local registry returned unsafe or incorrect object")
	}
	h := sha256.New()
	var got bytes.Buffer
	w := io.Writer(h)
	if original != nil {
		w = io.MultiWriter(h, &got)
	}
	n, err := io.Copy(w, io.LimitReader(res.Body, int64(d.Size)+1))
	if err != nil || uint64(n) != d.Size || "sha256:"+hex.EncodeToString(h.Sum(nil)) != d.Digest || original != nil && !bytes.Equal(got.Bytes(), original) {
		return errors.New("local registry object size, digest or original bytes mismatch")
	}
	return nil
}
