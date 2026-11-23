package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeDesktopLedger(t *testing.T, dataDir, tenant string, items []desktopReleaseItem) string {
	t.Helper()
	dir := filepath.Join(dataDir, "desktop", tenant)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	payload := map[string]any{
		"highestSequence": len(items),
		"items":           items,
		"audit":           []any{},
	}
	raw, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "state.json"), raw, 0644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func writeDesktopBlob(t *testing.T, tenantDir, sha string, content []byte) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(tenantDir, sha+".exe"), content, 0644); err != nil {
		t.Fatal(err)
	}
}

func sha256Of(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func TestDesktopReleaseCheck_Pass(t *testing.T) {
	dataDir := t.TempDir()
	tenant := "11111111-2222-3333-4444-555555555555"
	blob := []byte("auriga-desktop-setup-payload-v1")
	sum := sha256Of(blob)
	dir := writeDesktopLedger(t, dataDir, tenant, []desktopReleaseItem{
		{
			Manifest: desktopReleaseManifest{
				Version:   "1.2.3",
				Sequence:  4,
				SizeBytes: int64(len(blob)),
				SHA256:    sum,
			},
			Withdrawn: false,
		},
	})
	writeDesktopBlob(t, dir, sum, blob)

	summary, err := desktopReleaseCheck(state{
		Data:                   dataDir,
		DesktopPublisherTenant: tenant,
	})
	if err != nil {
		t.Fatalf("expected PASS, got error: %v", err)
	}
	for _, want := range []string{"已发布 1.2.3", "sequence 4", "摘要校验通过"} {
		if !strings.Contains(summary, want) {
			t.Fatalf("summary %q missing %q", summary, want)
		}
	}
}

func TestDesktopReleaseCheck_FallbackStackEnvTenant(t *testing.T) {
	dataDir := t.TempDir()
	tenant := "11111111-2222-3333-4444-555555555555"
	if err := os.WriteFile(filepath.Join(dataDir, "stack.env"), []byte("AURIGA_DESKTOP_PUBLISHER_TENANT_ID="+tenant+"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	blob := []byte("auriga-desktop-env-fallback")
	sum := sha256Of(blob)
	dir := writeDesktopLedger(t, dataDir, tenant, []desktopReleaseItem{
		{
			Manifest: desktopReleaseManifest{
				Version:   "1.0.0",
				Sequence:  1,
				SizeBytes: int64(len(blob)),
				SHA256:    sum,
			},
		},
	})
	writeDesktopBlob(t, dir, sum, blob)

	summary, err := desktopReleaseCheck(state{Data: dataDir})
	if err != nil {
		t.Fatalf("expected PASS with stack.env fallback, got error: %v", err)
	}
	if !strings.Contains(summary, "已发布 1.0.0") {
		t.Fatalf("unexpected summary: %q", summary)
	}
}

func TestDesktopReleaseCheck_MissingStateJSON(t *testing.T) {
	dataDir := t.TempDir()
	_, err := desktopReleaseCheck(state{
		Data:                   dataDir,
		DesktopPublisherTenant: "11111111-2222-3333-4444-555555555555",
	})
	if err == nil || !strings.Contains(err.Error(), "state.json") {
		t.Fatalf("expected missing state.json error, got: %v", err)
	}
}

func TestDesktopReleaseCheck_MissingTenant(t *testing.T) {
	dataDir := t.TempDir()
	_, err := desktopReleaseCheck(state{Data: dataDir})
	if err == nil || !strings.Contains(err.Error(), "桌面发行租户") {
		t.Fatalf("expected missing tenant error, got: %v", err)
	}
}

func TestDesktopReleaseCheck_AllWithdrawn(t *testing.T) {
	dataDir := t.TempDir()
	tenant := "11111111-2222-3333-4444-555555555555"
	blob := []byte("withdrawn-payload")
	sum := sha256Of(blob)
	dir := writeDesktopLedger(t, dataDir, tenant, []desktopReleaseItem{
		{
			Manifest: desktopReleaseManifest{
				Version:   "1.0.0",
				Sequence:  1,
				SizeBytes: int64(len(blob)),
				SHA256:    sum,
			},
			Withdrawn: true,
		},
		{
			Manifest: desktopReleaseManifest{
				Version:   "1.1.0",
				Sequence:  2,
				SizeBytes: int64(len(blob)),
				SHA256:    sum,
			},
			Withdrawn: true,
		},
	})
	writeDesktopBlob(t, dir, sum, blob)

	_, err := desktopReleaseCheck(state{
		Data:                   dataDir,
		DesktopPublisherTenant: tenant,
	})
	if err == nil || !strings.Contains(err.Error(), "未撤回") {
		t.Fatalf("expected all-withdrawn error, got: %v", err)
	}
}

func TestDesktopReleaseCheck_MissingBlob(t *testing.T) {
	dataDir := t.TempDir()
	tenant := "11111111-2222-3333-4444-555555555555"
	sum := sha256Of([]byte("missing"))
	writeDesktopLedger(t, dataDir, tenant, []desktopReleaseItem{
		{
			Manifest: desktopReleaseManifest{
				Version:   "1.0.0",
				Sequence:  1,
				SizeBytes: 7,
				SHA256:    sum,
			},
		},
	})

	_, err := desktopReleaseCheck(state{
		Data:                   dataDir,
		DesktopPublisherTenant: tenant,
	})
	if err == nil || !strings.Contains(err.Error(), "缺失") {
		t.Fatalf("expected missing blob error, got: %v", err)
	}
}

func TestDesktopReleaseCheck_SizeMismatch(t *testing.T) {
	dataDir := t.TempDir()
	tenant := "11111111-2222-3333-4444-555555555555"
	blob := []byte("actual-content")
	sum := sha256Of(blob)
	dir := writeDesktopLedger(t, dataDir, tenant, []desktopReleaseItem{
		{
			Manifest: desktopReleaseManifest{
				Version:   "1.0.0",
				Sequence:  1,
				SizeBytes: int64(len(blob) + 10),
				SHA256:    sum,
			},
		},
	})
	writeDesktopBlob(t, dir, sum, blob)

	_, err := desktopReleaseCheck(state{
		Data:                   dataDir,
		DesktopPublisherTenant: tenant,
	})
	if err == nil || !strings.Contains(err.Error(), "大小不符") {
		t.Fatalf("expected size mismatch error, got: %v", err)
	}
}

func TestDesktopReleaseCheck_SHA256Mismatch(t *testing.T) {
	dataDir := t.TempDir()
	tenant := "11111111-2222-3333-4444-555555555555"
	expected := []byte("expected-bytes")
	tampered := []byte("tampered-bytes") // same length, different hash
	sum := sha256Of(expected)
	dir := writeDesktopLedger(t, dataDir, tenant, []desktopReleaseItem{
		{
			Manifest: desktopReleaseManifest{
				Version:   "1.0.0",
				Sequence:  1,
				SizeBytes: int64(len(expected)),
				SHA256:    sum,
			},
		},
	})
	writeDesktopBlob(t, dir, sum, tampered)

	_, err := desktopReleaseCheck(state{
		Data:                   dataDir,
		DesktopPublisherTenant: tenant,
	})
	if err == nil || !strings.Contains(err.Error(), "SHA-256 不符") {
		t.Fatalf("expected sha256 mismatch error, got: %v", err)
	}
}

func TestDesktopReleaseCheck_PicksLatestActiveSequence(t *testing.T) {
	dataDir := t.TempDir()
	tenant := "11111111-2222-3333-4444-555555555555"
	blob1 := []byte("v1-binary")
	blob2 := []byte("v2-binary-latest-active")
	blob3 := []byte("v3-withdrawn")
	sum1, sum2, sum3 := sha256Of(blob1), sha256Of(blob2), sha256Of(blob3)
	dir := writeDesktopLedger(t, dataDir, tenant, []desktopReleaseItem{
		{
			Manifest: desktopReleaseManifest{
				Version:   "1.0.0",
				Sequence:  1,
				SizeBytes: int64(len(blob1)),
				SHA256:    sum1,
			},
			Withdrawn: false,
		},
		{
			Manifest: desktopReleaseManifest{
				Version:   "2.0.0",
				Sequence:  3,
				SizeBytes: int64(len(blob3)),
				SHA256:    sum3,
			},
			Withdrawn: true,
		},
		{
			Manifest: desktopReleaseManifest{
				Version:   "1.5.0",
				Sequence:  2,
				SizeBytes: int64(len(blob2)),
				SHA256:    sum2,
			},
			Withdrawn: false,
		},
	})
	// 仅写入最新未撤回的 sequence 2 的安装包即可通过校验
	writeDesktopBlob(t, dir, sum2, blob2)

	summary, err := desktopReleaseCheck(state{
		Data:                   dataDir,
		DesktopPublisherTenant: tenant,
	})
	if err != nil {
		t.Fatalf("expected PASS for latest active sequence 2, got error: %v", err)
	}
	if !strings.Contains(summary, "已发布 1.5.0（sequence 2") {
		t.Fatalf("expected sequence 2 summary, got: %q", summary)
	}
}
