package main

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestNormalizeSharedStorageSpec(t *testing.T) {
	def, err := normalizeSharedStorageSpec("", "", "", 0, 0)
	if err != nil {
		t.Fatalf("default spec rejected: %v", err)
	}
	if def.Backend != "" || def.QuotaMount != "" || def.QuotaBinary != "" || def.ProjectFirst != 0 || def.ProjectLast != 0 {
		t.Fatalf("unexpected default spec: %+v", def)
	}
	first, last := def.effectiveProjectRange()
	if first != sharedStorageDefaultProjectFirst || last != sharedStorageDefaultProjectLast {
		t.Fatalf("unexpected default project range: %d..%d", first, last)
	}

	ext4, err := normalizeSharedStorageSpec("ext4-image", "", "", 20000, 30000)
	if err != nil {
		t.Fatalf("explicit ext4-image rejected: %v", err)
	}
	if ext4.Backend != "" || ext4.ProjectFirst != 20000 || ext4.ProjectLast != 30000 {
		t.Fatalf("unexpected ext4 spec: %+v", ext4)
	}

	xfsDef, err := normalizeSharedStorageSpec("xfs", "/srv/auriga/auriga-data/data/agent/pools", "", 0, 0)
	if err != nil {
		t.Fatalf("xfs default binary rejected: %v", err)
	}
	if xfsDef.Backend != "xfs" || xfsDef.QuotaMount != "/srv/auriga/auriga-data/data/agent/pools" || xfsDef.QuotaBinary != sharedStorageDefaultXFSBinary {
		t.Fatalf("unexpected xfs spec: %+v", xfsDef)
	}

	xfsCustom, err := normalizeSharedStorageSpec("xfs", "/srv/auriga/auriga-data/data/agent/pools", "/sbin/xfs_quota", 10000, 199999)
	if err != nil {
		t.Fatalf("xfs custom rejected: %v", err)
	}
	if xfsCustom.QuotaBinary != "/sbin/xfs_quota" || xfsCustom.ProjectFirst != 10000 || xfsCustom.ProjectLast != 199999 {
		t.Fatalf("unexpected custom xfs spec: %+v", xfsCustom)
	}

	for name, tc := range map[string]struct {
		backend, mount, bin string
		first, last         uint32
	}{
		"unknown-backend":   {"btrfs", "", "", 0, 0},
		"ext4-with-mount":   {"ext4-image", "/srv/pools", "", 0, 0},
		"ext4-with-binary":  {"", "", "/sbin/xfs_quota", 0, 0},
		"xfs-missing-mount": {"xfs", "", "", 0, 0},
		"xfs-root-mount":    {"xfs", "/", "", 0, 0},
		"xfs-relative":      {"xfs", "relative/pools", "", 0, 0},
		"xfs-dotdot":        {"xfs", "/srv/auriga/../pools", "", 0, 0},
		"xfs-trailing-slash": {"xfs", "/srv/auriga/pools/", "", 0, 0},
		"xfs-space":          {"xfs", "/srv/auriga/my pools", "", 0, 0},
		"xfs-comma":          {"xfs", "/srv/auriga/pools,bad", "", 0, 0},
		"xfs-nul":            {"xfs", "/srv/auriga/pools\x00bad", "", 0, 0},
		"xfs-vtab":           {"xfs", "/srv/auriga/pools\vbad", "", 0, 0},
		"xfs-formfeed":       {"xfs", "/srv/auriga/pools\fbad", "", 0, 0},
		"xfs-hash":           {"xfs", "/srv/auriga/pools#1", "", 0, 0},
		"xfs-semicolon":      {"xfs", "/srv/auriga/pools;id", "", 0, 0},
		"xfs-dollar":         {"xfs", "/srv/auriga/$pools", "", 0, 0},
		"xfs-non-ascii":      {"xfs", "/srv/auriga/共享池", "", 0, 0},
		"xfs-bad-binary":     {"xfs", "/srv/auriga/pools", "xfs_quota", 0, 0},
		"xfs-binary-hash":    {"xfs", "/srv/auriga/pools", "/usr/sbin/xfs#quota", 0, 0},
		"only-first":         {"xfs", "/srv/auriga/pools", "", 10000, 0},
		"only-last":          {"xfs", "/srv/auriga/pools", "", 0, 199999},
		"reversed-range":     {"xfs", "/srv/auriga/pools", "", 20000, 10000},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := normalizeSharedStorageSpec(tc.backend, tc.mount, tc.bin, tc.first, tc.last); err == nil {
				t.Fatalf("expected %s to fail", name)
			}
		})
	}

	// Stored receipts must be strictly canonical.
	for name, bad := range map[string]sharedInstallationReceipt{
		"noncanonical-ext4-backend": {Version: 1, StorageBackend: "ext4-image"},
		"xfs-missing-binary":        {Version: 1, StorageBackend: "xfs", QuotaMount: "/srv/auriga/auriga-data/data/agent/pools"},
	} {
		t.Run(name, func(t *testing.T) {
			r := bad
			if _, err := sharedStorageSpecFromReceipt(&r); err == nil {
				t.Fatalf("expected stored receipt %s to fail", name)
			}
		})
	}
}

func TestSharedReplayBinding_IncludesXFSStorageFields(t *testing.T) {
	base := sharedInstallationReceipt{
		Version:        1,
		Tenant:         "t",
		Node:           "n",
		StorageBackend: "xfs",
		QuotaMount:     "/srv/auriga/auriga-data/data/agent/pools",
		QuotaBinary:    "/usr/sbin/xfs_quota",
		ProjectFirst:   10000,
		ProjectLast:    199999,
		Model:          "m1",
		Profile:        "p1",
		Files:          "f1",
	}
	want, err := sharedReplayBinding(&base)
	if err != nil {
		t.Fatal(err)
	}
	rotated := base
	rotated.Model, rotated.Profile, rotated.Files = "m2", "p2", "f2"
	gotRotated, err := sharedReplayBinding(&rotated)
	if err != nil || gotRotated != want {
		t.Fatalf("rotation changed XFS replay binding: got %q want %q err=%v", gotRotated, want, err)
	}
	for name, mutate := range map[string]func(*sharedInstallationReceipt){
		"backend": func(r *sharedInstallationReceipt) { r.StorageBackend = "" },
		"mount":   func(r *sharedInstallationReceipt) { r.QuotaMount = "/srv/other/pools" },
		"binary":  func(r *sharedInstallationReceipt) { r.QuotaBinary = "/sbin/xfs_quota" },
		"first":   func(r *sharedInstallationReceipt) { r.ProjectFirst = 10001 },
		"last":    func(r *sharedInstallationReceipt) { r.ProjectLast = 199998 },
	} {
		changed := base
		mutate(&changed)
		got, err := sharedReplayBinding(&changed)
		if err != nil || got == want {
			t.Fatalf("storage field %s change did not alter replay binding", name)
		}
	}
}

func TestSharedServiceProjection_XFSBackend(t *testing.T) {
	source := sharedFixture(t)
	source["platform/uac-adapter.json"] = []byte(sharedApprovedAdapterFixture)
	source["public/profile.yml"] = []byte("synthetic-approved-profile")
	source["public/host.json"] = []byte(`{"publicOnly":true}`)
	edge := []byte(`{"issuer":"https://fixture.example.invalid","loginPreference":"team","iamRpc":{"address":"platform:9091","serverName":"platform","serviceTokenFile":"/run/auriga-internal/edge/service.jwt","timeoutMillis":2000},"iamHop":{"certFile":"/etc/auriga/pki/edge-iam.crt","keyFile":"/etc/auriga/pki/edge-iam.key","caFile":"/etc/auriga/pki/ca.crt","serverName":"platform"}}`)
	gateway := []byte(`{"origin":"http://bifrost:8080","memberPasswordFile":"/run/secrets/bifrost_member_password","iam":{"Target":"platform:9091"},"org":{"Target":"platform:9091"}}`)
	receipt := &sharedInstallationReceipt{
		Version:        1,
		Tenant:         "11111111-2222-4333-8444-555555555555",
		Node:           "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee",
		StorageBackend: "xfs",
		QuotaMount:     "/srv/auriga/auriga-data/data/agent/pools",
		QuotaBinary:    "/sbin/xfs_quota",
		ProjectFirst:   10000,
		ProjectLast:    199999,
	}
	files, err := sharedServiceProjection(sharedProjectionInputs{
		Receipt:       receipt,
		Source:        source,
		RuntimeJSON:   []byte(`{"syntheticRuntime":true}`),
		InternalCA:    []byte("synthetic-public-ca"),
		EdgeConfig:    edge,
		BifrostConfig: gateway,
		NATSConfig:    sharedRenderedNATSFixture(t),
	})
	if err != nil {
		t.Fatalf("sharedServiceProjection(xfs) failed: %v", err)
	}
	var agent map[string]any
	if err := json.Unmarshal(files["agent/agent.json"].Body, &agent); err != nil {
		t.Fatalf("unmarshal agent.json: %v", err)
	}
	prep, ok := agent["sharedDshPreparation"].(map[string]any)
	if !ok {
		t.Fatalf("missing sharedDshPreparation: %#v", agent)
	}
	if prep["backend"] != "xfs" || prep["quotaMount"] != "/srv/auriga/auriga-data/data/agent/pools" || prep["quotaBinary"] != "/sbin/xfs_quota" {
		t.Fatalf("unexpected xfs sharedDshPreparation: %#v", prep)
	}
	if prep["projectFirst"] != float64(10000) || prep["projectLast"] != float64(199999) {
		t.Fatalf("unexpected project range: %#v", prep)
	}
	for _, forbidden := range []string{"imageBytes", "mkfsBinary", "losetupBinary"} {
		if _, exists := prep[forbidden]; exists {
			t.Fatalf("xfs preparation must not contain %s: %#v", forbidden, prep)
		}
	}

	// Default receipt serialization must omit storage fields for backward compatibility.
	raw, err := json.Marshal(sharedInstallationReceipt{Version: 1})
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"storageBackend", "quotaMount", "quotaBinary", "projectFirst", "projectLast"} {
		if bytes.Contains(raw, []byte(field)) {
			t.Fatalf("default receipt unexpectedly serialized %s: %s", field, raw)
		}
	}
}
