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

// XFSProjectQuota allocates unique IDs from a private installation ledger.
// Kernel quotas are installed/read back before a workspace is published.
type XFSProjectQuota struct {
	Binary, Mount string
	IDs           *ProjectIDs
	run           func(context.Context, string) (string, error) // command-boundary test double
}

func (q *XFSProjectQuota) command(ctx context.Context, command string) (string, error) {
	if q.run != nil {
		return q.run(ctx, command)
	}
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
	if q.IDs == nil || bytes != MemberBytes || inodes != MemberInodes {
		return fmt.Errorf("reserved project ledger required")
	}
	return q.IDs.Use(path, func(id uint32) error { return q.ensure(ctx, path, id, bytes, inodes) })
}
func (q *XFSProjectQuota) ensure(ctx context.Context, path string, id uint32, bytes, inodes int64) error {
	if e := AssertUnderRoot(q.Mount, path); e != nil {
		return e
	}
	// xfs_quota has its own command parser: reject whitespace and metacharacters.
	for _, r := range path {
		if r != '/' && r != '-' && r != '_' && r != '.' && (r < '0' || r > '9') && (r < 'A' || r > 'Z') && (r < 'a' || r > 'z') {
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

func (q *XFSProjectQuota) Release(ctx context.Context, path string) error {
	if q.IDs == nil {
		return fmt.Errorf("reserved project ledger required")
	}
	return q.IDs.Release(path, func(id uint32) error {
		out, err := q.command(ctx, fmt.Sprintf("limit -p bsoft=0 bhard=0 isoft=0 ihard=0 %d", id))
		if err != nil {
			return err
		}
		if strings.TrimSpace(out) != "" {
			return fmt.Errorf("quota reset diagnostic")
		}
		for _, kind := range []string{"b", "i"} {
			out, err = q.command(ctx, "report -p -"+kind+" -N -n")
			if err != nil {
				return err
			}
			if err = idleXFSProject(out, id); err != nil {
				return err
			}
		}
		return nil
	})
}

func idleXFSProject(out string, id uint32) error {
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 4 {
			return fmt.Errorf("invalid quota cleanup report")
		}
		parsed, err := strconv.ParseUint(strings.TrimPrefix(fields[0], "#"), 10, 32)
		if err != nil {
			return fmt.Errorf("invalid quota cleanup report")
		}
		if uint32(parsed) != id {
			continue
		}
		// Open, unlinked files still consume quota: keep the reservation until the
		// kernel confirms both usage and limits are zero for blocks and inodes.
		for _, value := range fields[1:4] {
			n, err := strconv.ParseUint(value, 10, 64)
			if err != nil || n != 0 {
				return fmt.Errorf("quota project cleanup unconfirmed")
			}
		}
	}
	return nil
}
