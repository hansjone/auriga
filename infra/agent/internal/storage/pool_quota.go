package storage

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// XFSProjectQuota uses installation-owned, unique project assignments. No shell
// is invoked. Paths/IDs are never accepted from a placement request. Mount must
// be a dedicated XFS filesystem with project accounting and enforcement enabled.
// Each active workspace AND retained archive requires a distinct project ID.
type XFSProjectQuota struct {
	Binary, Mount string
	Projects      map[string]uint32
}

func (q *XFSProjectQuota) command(ctx context.Context, command string) (string, error) {
	if !filepath.IsAbs(q.Binary) || !filepath.IsAbs(q.Mount) {
		return "", fmt.Errorf("absolute trusted XFS configuration required")
	}
	cmd := exec.CommandContext(ctx, q.Binary, "-x", "-c", command, q.Mount)
	cmd.Env = append(os.Environ(), "LC_ALL=C")
	raw, e := cmd.CombinedOutput()
	if e != nil {
		return "", fmt.Errorf("XFS quota command failed: %w", e)
	}
	return string(raw), nil
}
func (q *XFSProjectQuota) Ensure(ctx context.Context, path string, bytes, inodes int64) error {
	id := q.Projects[path]
	if id == 0 || bytes != MemberBytes || inodes != MemberInodes {
		return fmt.Errorf("missing trusted quota assignment")
	}
	for p, v := range q.Projects {
		if p != path && v == id {
			return fmt.Errorf("duplicate project assignment")
		}
	}
	if e := AssertUnderRoot(q.Mount, path); e != nil {
		return e
	}
	// xfs_quota has its own command parser: reject whitespace and metacharacters.
	for _, r := range path {
		if !(r == '/' || r == '-' || r == '_' || r == '.' || r >= '0' && r <= '9' || r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z') {
			return fmt.Errorf("invalid XFS path")
		}
	}
	state, e := q.command(ctx, "state -p")
	if e != nil {
		return e
	}
	if !strings.Contains(state, "Accounting: ON") || !strings.Contains(state, "Enforcement: ON") {
		return fmt.Errorf("project hard quota not enforced")
	}
	commands := []string{fmt.Sprintf("project -s -p %s %d", path, id), fmt.Sprintf("limit -p bhard=%dk ihard=%d %d", bytes/1024, inodes, id)}
	for _, c := range commands {
		if _, e = q.command(ctx, c); e != nil {
			return e
		}
	}
	// project -c can print a bad inode without setting a nonzero exit code.
	// Accept only the expected success prologue/summary, with no diagnostics or
	// extra /etc/projects paths (which could silently share this member's quota).
	checked, e := q.command(ctx, fmt.Sprintf("project -c -p %s %d", path, id))
	if e != nil {
		return e
	}
	if e = checkXFSProject(checked, path, id); e != nil {
		return e
	}
	// Read back kernel limits. Default report block units are KiB, without -h.
	for _, kind := range []struct {
		flag string
		hard int64
	}{{"b", bytes / 1024}, {"i", inodes}} {
		out, e := q.command(ctx, "report -p -"+kind.flag+" -N -n")
		if e != nil {
			return e
		}
		found := false
		for _, line := range strings.Split(out, "\n") {
			fields := strings.Fields(line)
			if len(fields) < 4 || strings.TrimPrefix(fields[0], "#") != strconv.FormatUint(uint64(id), 10) {
				continue
			}
			hard, e := strconv.ParseInt(fields[3], 10, 64)
			if e == nil && hard == kind.hard {
				found = true
			}
		}
		if !found {
			return fmt.Errorf("project hard limit readback failed")
		}
	}
	return nil
}

func checkXFSProject(out, path string, id uint32) error {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	summary := regexp.MustCompile(fmt.Sprintf(`^Processed 1 \(.+\) paths for project %d with recursion depth infinite \(-1\)\.$`, id))
	if len(lines) != 2 || strings.TrimSpace(lines[0]) != fmt.Sprintf("Checking project %d (path %s)...", id, path) || !summary.MatchString(strings.TrimSpace(lines[1])) {
		return fmt.Errorf("project tree assignment not confirmed")
	}
	return nil
}
