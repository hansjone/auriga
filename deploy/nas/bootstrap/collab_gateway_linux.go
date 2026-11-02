package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func nasGatewayBudget(raw []byte) (int, error) {
	var value map[string]json.RawMessage
	if e := nasRotationDecode(raw, &value); e != nil {
		return 0, e
	}
	var ms int
	if json.Unmarshal(value["operationTimeoutMillis"], &ms) != nil || ms < 10000 || ms > 120000 {
		return 0, fmt.Errorf("activation requires the existing checked network budget")
	}
	return ms, nil
}

// Do not rewrite network-policy or sealed installs. Publish an immutable,
// content-addressed projection within the controller's existing operations
// boundary. Both bind sources then change together, including after
// a crash; an old container cannot retain a replaced runtime.json inode.
func (r *nasRotation) gatewayRuntime(dir string) (string, error) {
	policy, e := nasRotationRead(filepath.Join(r.data, "config/network-policy/runtime.json"), 10001)
	if e != nil {
		return "", e
	}
	ms, e := nasGatewayBudget(policy)
	if e != nil {
		return "", e
	}
	source, e := nasRotationRead(filepath.Join(dir, "network/runtime.json"), 10001)
	if e != nil {
		return "", e
	}
	projected, e := projectNetworkRuntimeBudget(source, ms)
	if e != nil {
		return "", e
	}
	parent := filepath.Join(r.root, "gateway", filepath.Base(dir))
	for _, p := range []string{r.root, filepath.Dir(parent), parent} {
		if p != r.root {
			if e = os.Mkdir(p, 0700); e != nil && !os.IsExist(e) {
				return "", e
			}
		}
		if e = nasRotationDirectory(p, 0); e != nil {
			return "", e
		}
		if e = nasRotationSync(filepath.Dir(p)); e != nil {
			return "", e
		}
	}
	path := filepath.Join(parent, nasRotationHash(projected)+".json")
	if previous, err := nasRotationRead(path, 10001); err == nil {
		if !bytes.Equal(previous, projected) {
			return "", fmt.Errorf("gateway projection changed")
		}
	} else if !os.IsNotExist(err) {
		return "", err
	} else if e = writeBudgetProjection(path, projected, 10001); e != nil {
		return "", e
	}
	return path, nil
}

func (r *nasRotation) gatewayProjectionVersion(path string) string {
	rel, e := filepath.Rel(filepath.Join(r.root, "gateway"), path)
	parts := strings.Split(filepath.ToSlash(rel), "/")
	if e != nil || len(parts) != 2 || !nasRotationVersion(parts[0]) || !strings.HasSuffix(parts[1], ".json") || !nasRotationDigest.MatchString(strings.TrimSuffix(parts[1], ".json")) {
		return ""
	}
	if path != filepath.Join(r.root, "gateway", parts[0], parts[1]) {
		return ""
	}
	return parts[0]
}

func (r *nasRotation) checkGatewayRuntime(path, dir string) error {
	if r.gatewayProjectionVersion(path) != filepath.Base(dir) {
		return fmt.Errorf("gateway projection version differs from installation")
	}
	for _, p := range []string{r.root, filepath.Join(r.root, "gateway"), filepath.Dir(path)} {
		if e := nasRotationDirectory(p, 0); e != nil {
			return e
		}
	}
	raw, e := nasRotationRead(path, 10001)
	if e != nil {
		return e
	}
	if filepath.Base(path) != nasRotationHash(raw)+".json" {
		return fmt.Errorf("gateway projection digest changed")
	}
	ms, e := nasGatewayBudget(raw)
	if e != nil {
		return e
	}
	source, e := nasRotationRead(filepath.Join(dir, "network/runtime.json"), 10001)
	if e != nil {
		return e
	}
	expected, e := projectNetworkRuntimeBudget(source, ms)
	if e != nil {
		return e
	}
	if !bytes.Equal(raw, expected) {
		return fmt.Errorf("gateway projection differs from receipted installation")
	}
	return nil
}
