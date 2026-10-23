package releaseverify

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	nodev1 "github.com/hansjone/auriga/infra/gen/auriga/node/v1"
)

func TestCanonicalReleaseBodyExportMatchesRT0(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "contracts", "runtime", "signature-vectors.json"))
	if err != nil {
		t.Fatal(err)
	}
	var vectors struct{ Cases []vector }
	if err := json.Unmarshal(raw, &vectors); err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, v := range vectors.Cases {
		if v.Schema != "runtime" {
			continue
		}
		count++
		t.Run(v.Name, func(t *testing.T) {
			var d struct {
				SchemaVersion                                                               uint32
				Purpose, Name, ArtifactID, DSHVersion, OS, Arch, ArchiveDigest, ImageDigest string
				ReleaseSequence                                                             uint64
				Signature                                                                   struct{ Alg, Kid, Value string }
			}
			if err := json.Unmarshal(v.Document, &d); err != nil {
				t.Fatal(err)
			}
			e := &nodev1.RuntimeReleaseEnvelope{SchemaVersion: d.SchemaVersion, Purpose: d.Purpose, Name: d.Name, ArtifactId: d.ArtifactID, DshVersion: d.DSHVersion, Os: d.OS, Arch: d.Arch, ArchiveDigest: d.ArchiveDigest, ImageDigest: d.ImageDigest, ReleaseSequence: d.ReleaseSequence, IssuedAt: v.Timestamp, Signature: &nodev1.RuntimeReleaseSignature{Alg: d.Signature.Alg, Kid: d.Signature.Kid, Value: d.Signature.Value}}
			body, err := CanonicalReleaseBody(e)
			if err != nil || string(body) != v.Canonical {
				t.Fatalf("export changed RT0 canonical body: %v", err)
			}
		})
	}
	if count < 7 {
		t.Fatalf("missing runtime vectors: %d", count)
	}
	if _, err := CanonicalReleaseBody(nil); err == nil {
		t.Fatal("nil envelope accepted")
	}
}
