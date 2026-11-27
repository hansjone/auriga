package runtime

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExtractUnifiedSkillWithScriptsAndDependencies(t *testing.T) {
	raw := skillArchive(t, map[string]string{
		"SKILL.md":         unifiedSkillText,
		"requirements.txt": "requests>=2.32\n",
		"scripts/run.py":   "print('ok')\n",
		"scripts/run.sh":   "echo ok\n",
		"assets/data.toml": "key = 'value'\n",
	})
	dir := filepath.Join(t.TempDir(), "staged")
	if err := ExtractUnifiedSkill(raw, dir); err != nil {
		t.Fatalf("ExtractUnifiedSkill rejected valid scripted skill: %v", err)
	}
	for _, rel := range []string{"SKILL.md", "requirements.txt", "scripts/run.py", "scripts/run.sh", "assets/data.toml"} {
		info, err := os.Stat(filepath.Join(dir, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatalf("expected extracted file %s: %v", rel, err)
		}
		if info.Mode().Perm()&0111 != 0 {
			t.Fatalf("expected non-executable mode for %s, got %o", rel, info.Mode().Perm())
		}
	}
	for _, bad := range []string{"assets/payload.sh", "references/run.py", "scripts/bin.exe", "other/a.txt"} {
		badRaw := skillArchive(t, map[string]string{
			"SKILL.md": unifiedSkillText,
			bad:        "bad",
		})
		if err := ExtractUnifiedSkill(badRaw, filepath.Join(t.TempDir(), "bad")); err == nil {
			t.Fatalf("expected ExtractUnifiedSkill to reject %s", bad)
		}
	}
}
