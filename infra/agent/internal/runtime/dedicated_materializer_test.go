package runtime

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"github.com/hansjone/auriga/infra/agent/internal/sharednode"
	"os"
	"path/filepath"
	"strings"
	"testing"

	nodev1 "github.com/hansjone/auriga/infra/gen/auriga/node/v1"
)

func dedicatedSkillFixture(t *testing.T) *unifiedFixture {
	return newUnifiedSkillFixture(t, strings.Replace(unifiedSkillText, "Public isolated skill fixture.", "Fixture", 1), "Public fixture body.", nil)
}

func TestDedicatedMaterializerAcceptsSignedOptionalSkillAndPlugin(t *testing.T) {
	for _, kind := range []string{"skill", "plugin"} {
		t.Run(kind, func(t *testing.T) {
			f := dedicatedSkillFixture(t)
			if kind == "plugin" {
				f = newUnifiedPluginFixture(t)
			}
			m, s, views := poolSignedFixtureWith(t, f)
			views = views[:1]
			views[0].Binding.Mode = nodev1.DshInstanceMode_DSH_INSTANCE_MODE_DEDICATED
			s.f.ref.InstallationMode = "optional"
			if err := m.Verify(t.Context(), views[0].Binding.PhysicalInstanceId, views); err != nil {
				t.Fatal("current explicit dedicated optional resource rejected", err)
			}
			pool := views[0].Binding.PhysicalInstanceId
			cache, digest, err := m.DedicatedIntent(pool, views[0])
			if err != nil {
				t.Fatal(err)
			}
			_, file, err := m.dedicatedPaths(pool)
			if err != nil {
				t.Fatal(err)
			}
			binding, err := sharednode.HostValue(views[0].Binding)
			if err != nil {
				t.Fatal(err)
			}
			body, err := json.Marshal(binding)
			if err != nil {
				t.Fatal(err)
			}
			// Export the actual Go artifact for the separate public Host consumer
			// fixture. Ordinary Go CI does not need the full DSH SDK installation.
			if output := os.Getenv("AURIGA_DEDICATED_FIXTURE_OUTPUT"); output != "" {
				files := map[string]string{}
				err = filepath.WalkDir(cache, func(path string, entry os.DirEntry, err error) error {
					if err != nil {
						return err
					}
					if entry.IsDir() {
						return nil
					}
					raw, err := os.ReadFile(path)
					if err != nil {
						return err
					}
					relative, err := filepath.Rel(cache, path)
					if err != nil {
						return err
					}
					files[filepath.ToSlash(relative)] = base64.StdEncoding.EncodeToString(raw)
					return nil
				})
				if err != nil {
					t.Fatal(err)
				}
				intent, err := os.ReadFile(file)
				if err != nil {
					t.Fatal(err)
				}
				fixture, err := json.Marshal(map[string]any{"binding": json.RawMessage(body), "intent": string(intent), "files": files, "intentDigest": digest, "kind": kind, "now": f.now.Unix()})
				if err != nil {
					t.Fatal(err)
				}
				if !filepath.IsAbs(output) {
					t.Fatal("absolute fixture output directory required")
				}
				if err = os.MkdirAll(output, 0755); err != nil {
					t.Fatal(err)
				}
				if err = os.WriteFile(filepath.Join(output, kind+".json"), fixture, 0644); err != nil {
					t.Fatal(err)
				}
			}
			before, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			if err = m.Verify(t.Context(), pool, views); err != nil {
				t.Fatal("same selection did not reopen", err)
			}
			if kind == "skill" {
				if err = os.WriteFile(filepath.Join(cache, f.ref.Artifacts[0].Digest[7:], "skill", "SKILL.md"), []byte("tampered"), 0644); err != nil {
					t.Fatal(err)
				}
			} else {
				if err = os.WriteFile(filepath.Join(cache, f.ref.Artifacts[0].Digest[7:], "payload.tgz"), []byte("tampered"), 0644); err != nil {
					t.Fatal(err)
				}
			}
			if err = m.Verify(t.Context(), pool, views); err == nil {
				t.Fatal("changed readonly dedicated bytes accepted")
			}
			after, err := os.ReadFile(file)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatal("failed candidate overwrote current intent", err)
			}
		})
	}
}

func TestDedicatedMaterializerRemovalPreservesRevisionAndRejectsReplay(t *testing.T) {
	m, s, views := poolSignedFixtureWith(t, dedicatedSkillFixture(t))
	views = views[:1]
	view := views[0]
	view.Binding.Mode = nodev1.DshInstanceMode_DSH_INSTANCE_MODE_DEDICATED
	s.f.ref.InstallationMode = "optional"
	pool := view.Binding.PhysicalInstanceId
	if err := m.Verify(t.Context(), pool, views); err != nil {
		t.Fatal(err)
	}
	resource := view.Resources[0]
	view.Resources = []*nodev1.SharedDshResource{}
	view.ViewId = "00000000-0000-4000-8000-000000000021"
	if err := m.Verify(t.Context(), pool, views); err != nil {
		t.Fatal("remove absent optional selection", err)
	}
	if _, _, err := m.DedicatedIntent(pool, view); err != nil {
		t.Fatal(err)
	}
	view.Resources = []*nodev1.SharedDshResource{resource}
	view.ViewId = "00000000-0000-4000-8000-000000000022"
	if err := m.Verify(t.Context(), pool, views); err == nil {
		t.Fatal("collected installation revision replay accepted")
	}
	s.f.ref.InstallationRevision++
	if err := m.Verify(t.Context(), pool, views); err != nil {
		t.Fatal("newer enabled selection rejected", err)
	}
}

func TestDedicatedMaterializerRejectsSignedSkillDescriptionMismatch(t *testing.T) {
	m, _, views := poolSignedFixture(t)
	views = views[:1]
	views[0].Binding.Mode = nodev1.DshInstanceMode_DSH_INSTANCE_MODE_DEDICATED
	if err := m.Verify(t.Context(), views[0].Binding.PhysicalInstanceId, views); err == nil || err.Error() != "dedicated skill entry identity mismatch" {
		t.Fatal("signed manifest/frontmatter mismatch accepted", err)
	}
}
