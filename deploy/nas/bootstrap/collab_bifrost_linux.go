package main

import (
	"bytes"
	"fmt"
	"io"
	"path/filepath"
	"regexp"
)

var nasBifrostComposeDirectory = regexp.MustCompile(`^\.auriga-(compose|runtime-compose)\.[A-Za-z0-9]+$`)

// Compose removes these private files after creating the services. Accept only
// the fixed generated name in an immediate installation sibling, and only while
// the additive Bifrost activation is independently valid.
func nasBifrostComposeOverlay(data, path string) bool {
	return filepath.Clean(path) == path && filepath.IsAbs(path) &&
		filepath.Base(path) == "bifrost.yml" &&
		filepath.Dir(filepath.Dir(path)) == filepath.Dir(data) &&
		nasBifrostComposeDirectory.MatchString(filepath.Base(filepath.Dir(path)))
}

func checkNASBifrostActivation(config string) error {
	return nasMaterialsUpgrade([]string{
		"-action", "check", "-base", filepath.Join(config, "materials"),
		"-out", filepath.Join(config, "materials-upgrades"),
	}, io.Discard)
}

// A path label alone does not authorize a new control mount. Verify the current
// upgrade chain and compare its bytes with a projection of the original sealed
// base, or the independently sealed message source. Never regenerate it here.
func checkNASBifrostEdge(config string, allowCore bool) error {
	if err := checkNASBifrostActivation(config); err != nil {
		return err
	}
	projection := filepath.Join(config, "bifrost")
	if err := materialsPrivateDirectory(projection); err != nil {
		return err
	}
	raw, err := runtimeReadLeaf(filepath.Join(projection, "edge.config.json"), 0644, 1<<20)
	if err != nil {
		return err
	}
	base := filepath.Join(config, "materials")
	if allowCore {
		expected, err := bifrostEdgeProjection(filepath.Join(base, "edge.config.json"))
		if err != nil {
			return err
		}
		if bytes.Equal(raw, expected) {
			return nil
		}
	}
	baseHash, err := verifyNASCollabBase(base)
	if err != nil {
		return err
	}
	source := filepath.Join(config, "collab-source")
	if _, err := checkNASCollabSource(source, baseHash); err != nil {
		return err
	}
	expected, err := bifrostEdgeProjection(filepath.Join(source, "edge.config.json"))
	if err != nil {
		return err
	}
	if !bytes.Equal(raw, expected) {
		return fmt.Errorf("NAS Bifrost edge differs from sealed projection")
	}
	return nil
}
