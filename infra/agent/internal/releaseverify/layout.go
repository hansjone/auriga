package releaseverify

import "fmt"

const MaxLayoutMetadataBytes = 64 << 10

const LayoutVersionJSON = "{\"imageLayoutVersion\":\"1.0.0\"}\n"

// VerifyLayoutMetadata accepts the strict single-image transport profile only.
// The index is transport metadata; it is never substituted for the signed M.
func VerifyLayoutMetadata(layout, index []byte, image VerifiedImage) error {
	if !image.Valid() {
		return fmt.Errorf("verified image required")
	}
	l, err := strictJSON(layout, MaxLayoutMetadataBytes)
	if err != nil {
		return err
	}
	if err := canonicalMembers(l, "imageLayoutVersion"); err != nil {
		return err
	}
	if !only(l, "imageLayoutVersion") || text(l, "imageLayoutVersion") != "1.0.0" {
		return fmt.Errorf("unsupported OCI image layout")
	}
	i, err := strictJSON(index, MaxLayoutMetadataBytes)
	if err != nil {
		return err
	}
	if err := canonicalMembers(i, "schemaVersion", "mediaType", "manifests", "annotations", "artifactType", "subject"); err != nil {
		return err
	}
	version, ok := integer(i, "schemaVersion")
	if !ok || version != 2 || !only(i, "schemaVersion", "mediaType", "manifests", "annotations") {
		return fmt.Errorf("unsupported OCI index")
	}
	if mt, present := i["mediaType"]; present && mt != "application/vnd.oci.image.index.v1+json" {
		return fmt.Errorf("unsupported OCI index media type")
	}
	if a, present := i["annotations"]; present && !stringMap(a) {
		return fmt.Errorf("invalid OCI index annotations")
	}
	manifests, ok := array(i, "manifests")
	if !ok || len(manifests) != 1 {
		return fmt.Errorf("exactly one manifest is required")
	}
	d, ok := manifests[0].(map[string]any)
	if !ok {
		return fmt.Errorf("invalid index descriptor")
	}
	if err := canonicalMembers(d, "mediaType", "digest", "size", "annotations", "urls", "platform", "artifactType"); err != nil {
		return err
	}
	m, _, _ := image.Descriptors()
	digest, size, err := descriptor(d, m.MediaType)
	if err != nil || digest != m.Digest || size != m.Size {
		return fmt.Errorf("index does not identify verified manifest")
	}
	return nil
}
