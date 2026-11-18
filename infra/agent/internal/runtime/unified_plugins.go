package runtime

import (
	"fmt"
	"regexp"
	"strings"

	resourcev1 "github.com/hansjone/auriga/infra/gen/auriga/resource/v1"
	"google.golang.org/protobuf/proto"
)

var unifiedNPMName = regexp.MustCompile(`^(?:@[a-z0-9][a-z0-9._-]*/)?[a-z0-9][a-z0-9._-]*$`)

func unifiedPluginName(name string) bool {
	return len(name) <= 214 && unifiedNPMName.MatchString(name) && name != "auriga-dsh" && !strings.HasPrefix(name, "@deepseek-ai/")
}

func unifiedPluginPolicy(s *unifiedNodeState) []map[string]any {
	plugins := []map[string]any{}
	for _, record := range s.Installations {
		var ref resourcev1.UnifiedResourceRef
		if proto.Unmarshal(record.Ref, &ref) != nil || ref.GetIdentity().GetKind() != "plugin" || record.Name == "" {
			continue
		}
		mode := ref.GetInstallationMode()
		if mode == "" {
			mode = "optional"
		}
		plugin := map[string]any{"name": record.Name, "version": ref.GetIdentity().GetVersion(), "desiredState": record.DesiredState, "installationMode": mode}
		if s.State == "active" && record.DesiredState == "ENABLED" && len(ref.GetArtifacts()) == 1 {
			digest := ref.GetArtifacts()[0].GetDigest()
			plugin["digest"] = digest
			plugin["path"] = "/run/auriga/catalog-plugins/" + strings.TrimPrefix(digest, "sha256:") + ".tgz"
		}
		plugins = append(plugins, plugin)
	}
	return plugins
}

type unifiedPluginObservation struct {
	Name, Version                       string
	Enabled, Installed, Loaded, Pending bool
	Error                               any
}

func unifiedObservePlugin(s *unifiedNodeState, record unifiedInstalled, ref *resourcev1.UnifiedResourceRef, plugins []unifiedPluginObservation) (string, error) {
	var found *unifiedPluginObservation
	for i := range plugins {
		if plugins[i].Name == record.Name {
			if found != nil {
				return "", fmt.Errorf("duplicate native plugin observation")
			}
			found = &plugins[i]
		}
	}
	active := found != nil && (found.Enabled || found.Loaded)
	if s.State == "private_only" {
		if active {
			return "", fmt.Errorf("isolated managed plugin still active")
		}
		if record.DesiredState == "ENABLED" {
			if s.Reason == "revoked" {
				return "revoked", nil
			}
			return "expired", nil
		}
	}
	if record.DesiredState != "ENABLED" {
		if active || record.DesiredState == "REMOVED" && found != nil && found.Installed {
			return "", fmt.Errorf("managed plugin stop not observed")
		}
		return strings.ToLower(record.DesiredState), nil
	}
	if s.State == "active" && found != nil && found.Error == nil && found.Enabled && found.Installed && found.Pending && found.Version == ref.GetIdentity().GetVersion() {
		return "applying", nil
	}
	if s.State != "active" || found == nil || found.Error != nil || !found.Enabled || !found.Installed || !found.Loaded || found.Version != ref.GetIdentity().GetVersion() {
		return "", fmt.Errorf("native plugin observation disagrees with selection")
	}
	return "applied", nil
}
