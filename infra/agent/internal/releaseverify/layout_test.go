package releaseverify

import (
	"fmt"
	"strings"
	"testing"
)

func TestSingleImageLayoutMetadata(t *testing.T) {
	d := "sha256:" + strings.Repeat("a", 64)
	image := VerifiedImage{valid: true, manifest: BlobDescriptor{Digest: d, Size: 123, MediaType: ociManifest}}
	good := fmt.Sprintf(`{"schemaVersion":2,"mediaType":"application/vnd.oci.image.index.v1+json","manifests":[{"mediaType":%q,"digest":%q,"size":123,"annotations":{"org.opencontainers.image.ref.name":"export"}}]}`, ociManifest, d)
	if err := VerifyLayoutMetadata([]byte(LayoutVersionJSON), []byte(good), image); err != nil {
		t.Fatal(err)
	}
	bad := []string{
		strings.Replace(good, `"size":123`, `"size":124`, 1),
		strings.Replace(good, d, "sha256:"+strings.Repeat("b", 64), 1),
		strings.Replace(good, `"manifests":[`, `"manifests":[{"mediaType":"application/vnd.oci.image.manifest.v1+json","digest":"`+d+`","size":123},`, 1),
		strings.Replace(good, `"annotations":`, `"urls":["https://example.com"],"annotations":`, 1),
		strings.Replace(good, `"annotations":`, `"platform":{"os":"linux","architecture":"amd64"},"annotations":`, 1),
		strings.Replace(good, `"schemaVersion":2`, `"schemaVersion":2,"schemaVersion":2`, 1),
		strings.Replace(good, `"schemaVersion":2`, `"SchemaVersion":2`, 1),
		strings.Replace(good, `"size":123`, `"size":"123"`, 1),
		strings.Replace(good, `"manifests":`, `"subject":{},"manifests":`, 1),
		strings.Replace(good, `"manifests":`, `"artifactType":"x","manifests":`, 1),
	}
	for i, raw := range bad {
		if err := VerifyLayoutMetadata([]byte(LayoutVersionJSON), []byte(raw), image); err == nil {
			t.Fatalf("bad index %d accepted", i)
		}
	}
	for i, raw := range []string{`{"imageLayoutVersion":"1.1.0"}`, `{"ImageLayoutVersion":"1.0.0"}`, `{"imageLayoutVersion":"1.0.0","imageLayoutVersion":"1.0.0"}`, `{"imageLayoutVersion":1}`} {
		if err := VerifyLayoutMetadata([]byte(raw), []byte(good), image); err == nil {
			t.Fatalf("bad layout %d accepted", i)
		}
	}
	if err := VerifyLayoutMetadata([]byte(LayoutVersionJSON), []byte(good), VerifiedImage{}); err == nil {
		t.Fatal("unverified image accepted")
	}
}
