package app

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"

	"github.com/hansjone/auriga/infra/agent/internal/config"
	"github.com/hansjone/auriga/infra/agent/internal/ingress"
	"github.com/hansjone/auriga/infra/agent/internal/renderer"
	"github.com/hansjone/auriga/infra/agent/internal/runtime"
	"github.com/hansjone/auriga/infra/agent/internal/storage"
	nodev1 "github.com/hansjone/auriga/infra/gen/auriga/node/v1"
	"google.golang.org/protobuf/encoding/protojson"
)

func sharedRuntime(cfg config.Config, pools *runtime.SharedPools, docker *runtime.Docker, registry *ingress.MapRegistry) (*runtime.PersistentPools, error) {
	settings := cfg.SharedDshRuntime
	if settings == nil {
		return nil, nil
	}
	if cfg.AllowInsecure {
		return nil, fmt.Errorf("shared resource views require authenticated NodeControl")
	}
	if pools == nil || docker == nil || docker.Activation == nil {
		return nil, fmt.Errorf("shared runtime requires provisioned storage and signed Docker activation")
	}
	read := func(path string) ([]byte, error) {
		if !filepath.IsAbs(path) {
			return nil, fmt.Errorf("absolute shared installation file required")
		}
		info, err := os.Lstat(path)
		if err != nil {
			return nil, err
		}
		if !info.Mode().IsRegular() || info.Size() > 4*1024*1024 {
			return nil, fmt.Errorf("invalid shared installation file")
		}
		return os.ReadFile(path)
	}
	data, err := read(settings.TemplateFile)
	if err != nil {
		return nil, err
	}
	template := new(nodev1.InstanceSpec)
	if err = protojson.Unmarshal(data, template); err != nil {
		return nil, err
	}
	profile, err := read(settings.ProfileFile)
	if err != nil {
		return nil, err
	}
	if err = renderer.ValidatePoolProfile(profile); err != nil {
		return nil, err
	}
	ports, err := read(settings.PortsModuleFile)
	if err != nil {
		return nil, err
	}
	pools.Supervisor = docker
	return &runtime.PersistentPools{Pools: pools, Driver: docker,
		UIDs: storage.NewUIDTable(cfg.AgentDir()), Template: template, Profile: profile, Ports: ports,
		Resources: &runtime.EmptyPoolResources{Root: pools.Layout.Root},
		Publish: func(pool string, endpoint runtime.Endpoint) {
			upstream, err := url.Parse(endpoint.URL)
			if err == nil {
				registry.Put(&ingress.Instance{ID: pool, Shared: true, Upstream: upstream})
			}
		},
		Unpublish: registry.Delete,
	}, nil
}
