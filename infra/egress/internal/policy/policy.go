// Package policy consumes the trusted local, read-only deployment policy channel.
package policy

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

//go:generate node ../../../../services/contracts/scripts/gen-egress.mjs

var domain = regexp.MustCompile(`^[a-z0-9-]+(?:\.[a-z0-9-]+)+$`)
var tld = regexp.MustCompile(`^[a-z]{2,63}$`)
var tenant = regexp.MustCompile(`^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$`)
var node = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

// Both 256-item domain lists, including maximum length DNS names, fit this bound.
const maxSnapshotBytes = 256 * 1024

func Validate(value Value) error {
	if len(value.Categories) != 0 {
		return errors.New("unsupported egress category")
	}
	for _, list := range [][]string{value.AllowDomains, value.DenyDomains} {
		if len(list) > 256 {
			return errors.New("too many egress domains")
		}
		seen := map[string]bool{}
		for _, pattern := range list {
			host := strings.TrimPrefix(pattern, "*.")
			labels := strings.Split(host, ".")
			if len(host) > 253 || !domain.MatchString(host) || !tld.MatchString(labels[len(labels)-1]) || seen[pattern] {
				return errors.New("invalid egress domain")
			}
			seen[pattern] = true
			for _, label := range labels {
				if len(label) > 63 || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
					return errors.New("invalid egress label")
				}
			}
			for _, suffix := range []string{"localhost", "local", "internal", "home.arpa"} {
				if host == suffix || strings.HasSuffix(host, "."+suffix) {
					return errors.New("internal egress domain")
				}
			}
		}
	}
	return nil
}

func Match(pattern, host string) bool {
	if strings.HasPrefix(pattern, "*.") {
		return strings.HasSuffix(host, pattern[1:])
	}
	return pattern == host
}

type Applier interface{ ApplyPolicy(Value) }
type Runner struct {
	source, state, report, nodeID string
	apply                         Applier
	current                       Snapshot
	digest                        string
}

func New(source, state, report, nodeID string, apply Applier) (*Runner, error) {
	if source == "" || state == "" || report == "" || !node.MatchString(nodeID) || source == state || source == report || state == report {
		return nil, errors.New("invalid policy channel configuration")
	}
	r := &Runner{source: source, state: state, report: report, nodeID: nodeID, apply: apply}
	raw, err := read(state)
	if errors.Is(err, os.ErrNotExist) {
		return r, nil
	}
	if err != nil {
		return nil, err
	}
	snapshot, err := decode(raw)
	if err != nil {
		return nil, err
	}
	r.current = snapshot
	r.digest = hash(raw)
	apply.ApplyPolicy(snapshot.Value)
	return r, nil
}

func read(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > maxSnapshotBytes {
		return nil, errors.New("invalid policy channel file")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	return io.ReadAll(io.LimitReader(file, maxSnapshotBytes+1))
}
func decode(raw []byte) (Snapshot, error) {
	var s Snapshot
	if len(raw) > maxSnapshotBytes {
		return s, errors.New("oversized policy")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&s); err != nil {
		return s, err
	}
	if decoder.Decode(new(any)) != io.EOF {
		return s, errors.New("trailing policy data")
	}
	if s.SchemaVersion != 1 || s.Version < 1 || !tenant.MatchString(s.TenantId) {
		return s, errors.New("invalid policy identity")
	}
	return s, Validate(s.Value)
}
func hash(raw []byte) string { sum := sha256.Sum256(raw); return hex.EncodeToString(sum[:]) }
func atomic(path string, raw []byte) error {
	file, err := os.CreateTemp(filepath.Dir(path), ".egress-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(file.Name()) }()
	if _, err = file.Write(raw); err == nil {
		err = file.Chmod(0o644)
	}
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}

func (r *Runner) Sync() error {
	raw, err := read(r.source)
	if err != nil {
		return err
	}
	s, err := decode(raw)
	if err != nil {
		return err
	}
	digest := hash(raw)
	result := "applied"
	if r.current.Version > 0 && (s.TenantId != r.current.TenantId || s.Version < r.current.Version || (s.Version == r.current.Version && digest != r.digest)) {
		result = "failed"
	} else if s.Version > r.current.Version {
		if err = atomic(r.state, raw); err != nil {
			return errors.Join(err, r.writeReport(s, digest, "failed"))
		}
		r.apply.ApplyPolicy(s.Value)
		r.current = s
		r.digest = digest
	}
	if err = r.writeReport(s, digest, result); err != nil {
		return err
	}
	if result == "failed" {
		return fmt.Errorf("policy version regression or equivocation")
	}
	return nil
}

func (r *Runner) writeReport(s Snapshot, digest, result string) error {
	report := Report{TenantId: s.TenantId, Version: s.Version, NodeId: r.nodeID, Digest: digest, Result: result, AppliedVersion: r.current.Version, ReportedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	encoded, err := json.Marshal(report)
	if err != nil {
		return err
	}
	return atomic(r.report, encoded)
}

func (r *Runner) Run(ctx context.Context, interval time.Duration, onError func(error)) {
	tick := time.NewTicker(interval)
	defer tick.Stop()
	for {
		if err := r.Sync(); err != nil {
			onError(err)
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}
