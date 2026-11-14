package runtime

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/hansjone/auriga/infra/agent/internal/storage"
	resourcev1 "github.com/hansjone/auriga/infra/gen/auriga/resource/v1"
	"google.golang.org/protobuf/proto"
)

// Original bytes needed by current installations remain pinned. Historical
// migration evidence, receipts and watermarks have separate durable ownership
// and must not be removed by payload-cache collection.
func (m *UnifiedManager) collectResourceCache(root string, s *unifiedNodeState) error {
	pinned := map[string]bool{}
	for _, record := range s.Installations {
		var ref resourcev1.UnifiedResourceRef
		if err := proto.Unmarshal(record.Ref, &ref); err != nil {
			return err
		}
		pinned[strings.TrimPrefix(ref.GetIdentity().GetManifestDigest(), "sha256:")] = true
		for _, artifact := range ref.GetArtifacts() {
			pinned[strings.TrimPrefix(artifact.GetDigest(), "sha256:")] = true
		}
	}
	directory := filepath.Join(root, "cache")
	entries, err := os.ReadDir(directory)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() || !uDigest.MatchString("sha256:"+entry.Name()) {
			return fmt.Errorf("invalid resource cache entry")
		}
		if pinned[entry.Name()] {
			continue
		}
		target := filepath.Join(directory, entry.Name())
		if err := storage.CheckNoSymlink(root, target); err != nil {
			return err
		}
		if err := os.RemoveAll(target); err != nil {
			return err
		}
	}
	return nil
}
