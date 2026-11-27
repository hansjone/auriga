package runtime

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"
)

func skillArchiveWithModes(t *testing.T, entries []struct {
	name string
	body string
	mode int64
	dir  bool
}) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		hdr := &tar.Header{
			Name:   e.name,
			Mode:   e.mode,
			Format: tar.FormatUSTAR,
		}
		if e.dir {
			hdr.Typeflag = tar.TypeDir
			hdr.Size = 0
		} else {
			hdr.Typeflag = tar.TypeReg
			hdr.Size = int64(len(e.body))
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if !e.dir {
			if _, err := tw.Write([]byte(e.body)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestExtractUnifiedSkillWithScriptsAndDependencies(t *testing.T) {
	raw := skillArchiveWithModes(t, []struct {
		name string
		body string
		mode int64
		dir  bool
	}{
		{name: "scripts/", mode: 0755, dir: true},
		{name: "SKILL.md", body: unifiedSkillText, mode: 0644},
		{name: "requirements.txt", body: "requests>=2.32\n", mode: 0644},
		{name: "scripts/run.py", body: "print('ok')\n", mode: 0644},
		{name: "scripts/run.sh", body: "echo ok\n", mode: 0644},
		{name: "scripts/data.toml", body: "key = 'value'\n", mode: 0644},
	})
	dir := filepath.Join(t.TempDir(), "staged")
	if err := ExtractUnifiedSkill(raw, dir); err != nil {
		t.Fatalf("ExtractUnifiedSkill rejected valid scripted skill: %v", err)
	}
	for _, rel := range []string{"SKILL.md", "requirements.txt", "scripts/run.py", "scripts/run.sh", "scripts/data.toml"} {
		info, err := os.Stat(filepath.Join(dir, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatalf("expected extracted file %s: %v", rel, err)
		}
		if info.Mode().Perm()&0111 != 0 {
			t.Fatalf("expected non-executable mode for %s, got %o", rel, info.Mode().Perm())
		}
	}
	execRaw := skillArchiveWithModes(t, []struct {
		name string
		body string
		mode int64
		dir  bool
	}{
		{name: "SKILL.md", body: unifiedSkillText, mode: 0644},
		{name: "scripts/run.sh", body: "echo nope\n", mode: 0755},
	})
	if err := ExtractUnifiedSkill(execRaw, filepath.Join(t.TempDir(), "exec")); err == nil {
		t.Fatalf("expected ExtractUnifiedSkill to reject executable script mode 0755")
	}
	for _, bad := range []string{"assets/payload.sh", "references/run.py", "Scripts/run.py", "scripts/bin.exe", "other/a.txt"} {
		badRaw := skillArchive(t, map[string]string{
			"SKILL.md": unifiedSkillText,
			bad:        "bad",
		})
		if err := ExtractUnifiedSkill(badRaw, filepath.Join(t.TempDir(), "bad")); err == nil {
			t.Fatalf("expected ExtractUnifiedSkill to reject %s", bad)
		}
	}
}
