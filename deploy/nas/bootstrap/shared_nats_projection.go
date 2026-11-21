package main

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"
)

const sharedNATSUserMarker = "# __AURIGA_SHARED_USER__"
const sharedNATSPlatformMarker = "# __AURIGA_SHARED_PLATFORM_PERMISSIONS__"

// This consumes the standard freshly rendered password-account configuration,
// not an arbitrary operator/account-JWT replacement. Existing users and their
// passwords stay byte-for-byte intact. Only two reviewed template anchors may
// be replaced, and only the dedicated NKey user / W read permissions are added.
func sharedNATSProjection(original []byte, publicNKey string) ([]byte, error) {
	if len(original) == 0 || len(original) > 256<<10 || bytes.Count(original, []byte(sharedNATSUserMarker)) != 1 || bytes.Count(original, []byte(sharedNATSPlatformMarker)) != 1 || bytes.Contains(original, []byte("auriga-shared-entry")) {
		return nil, fmt.Errorf("fresh standard NATS configuration with unique shared anchors required")
	}
	user, err := sharedReplayNATSUser(publicNKey)
	if err != nil {
		return nil, err
	}
	var permissions strings.Builder
	for _, subject := range sharedReplayPlatformPermissions() {
		fmt.Fprintf(&permissions, "%s,\n            ", strconv.Quote(subject))
	}
	projected := bytes.Replace(original, []byte(sharedNATSUserMarker), []byte(user), 1)
	projected = bytes.Replace(projected, []byte(sharedNATSPlatformMarker), []byte(permissions.String()), 1)
	return projected, nil
}
