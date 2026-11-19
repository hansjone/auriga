package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sync"

	"github.com/hansjone/auriga/infra/agent/internal/config"
	"github.com/hansjone/auriga/infra/agent/internal/ingress"
	"github.com/hansjone/auriga/infra/agent/internal/renderer"
	"github.com/hansjone/auriga/infra/agent/internal/runtime"
	"github.com/hansjone/auriga/infra/agent/internal/sharednode"
	"github.com/hansjone/auriga/infra/agent/internal/storage"
	nodev1 "github.com/hansjone/auriga/infra/gen/auriga/node/v1"
	"google.golang.org/protobuf/encoding/protojson"
)

func sharedRuntime(ctx context.Context, cfg config.Config, pools *runtime.SharedPools, docker *runtime.Docker, registry *ingress.MapRegistry) (*runtime.PersistentPools, error) {
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
		if !info.Mode().IsRegular() || info.Mode().Perm()&0022 != 0 || info.Size() > 4*1024*1024 {
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
	if err = renderer.ValidateProductionPoolProfile(profile); err != nil {
		return nil, err
	}
	// Production uses the reviewed, signed package export, not a handwritten
	// mutable module installed outside the runtime image.
	if settings.PortsModuleFile != "" {
		return nil, fmt.Errorf("use auriga-dsh/shared-ports in the signed profile")
	}
	var ports []byte
	client, err := openSharedClient(cfg)
	if err != nil {
		return nil, err
	}
	go func() { <-ctx.Done(); client.Close() }()
	hostRaw, err := read(settings.HostConfigFile)
	if err != nil {
		return nil, err
	}
	var hostTemplate map[string]any
	if err = json.Unmarshal(hostRaw, &hostTemplate); err != nil {
		return nil, err
	}
	for _, field := range []string{"identityKeys", "capabilityKeys", "model", "enterprise"} {
		if hostTemplate[field] == nil {
			return nil, fmt.Errorf("shared host installation missing %s", field)
		}
	}
	for key := range hostTemplate {
		if key != "identityKeys" && key != "capabilityKeys" && key != "model" && key != "enterprise" && key != "messageTrust" {
			return nil, fmt.Errorf("unexpected shared Host template field %q", key)
		}
	}
	var hostMu sync.Mutex
	var manager *runtime.PersistentPools
	admission := &runtime.PoolTaskAdmission{Pools: pools, Driver: docker, TenantID: client.Settings.TenantID}
	admission.Report = func(sample *nodev1.SharedDshPoolMemorySample) {
		if manager != nil && manager.Report != nil {
			manager.Report(&nodev1.SessionRequest{Body: &nodev1.SessionRequest_SharedDshPoolMemorySample{SharedDshPoolMemorySample: sample}})
		}
	}
	started := map[string]string{}
	configure := func(_ context.Context, b *nodev1.SharedDshBinding, uid int) (string, error) {
		hostMu.Lock()
		defer hostMu.Unlock()
		if b.TenantId != client.Settings.TenantID {
			return "", fmt.Errorf("shared tenant mismatch")
		}
		if directory := started[b.PhysicalInstanceId]; directory != "" {
			return directory, nil
		}
		directory, err := pools.Layout.Path(b.PhysicalInstanceId, "control", "host-relay")
		if err != nil {
			return "", err
		}
		if err = os.MkdirAll(directory, 0755); err != nil {
			return "", err
		}
		var config map[string]any
		if err = json.Unmarshal(hostRaw, &config); err != nil {
			return "", err
		}
		target := "/var/lib/auriga/pools/" + b.PhysicalInstanceId + "/control"
		config["schemaVersion"], config["tenantId"], config["physicalInstanceId"] = 1, b.TenantId, b.PhysicalInstanceId
		config["nodeSocket"], config["skillRoots"] = target+"/shared-node.sock", []string{"/run/auriga/skills-active/current"}
		raw, err := json.Marshal(config)
		if err != nil || len(raw) > 256*1024 {
			return "", fmt.Errorf("invalid shared host configuration")
		}
		path := filepath.Join(directory, "shared-host.json")
		if old, e := os.ReadFile(path); e == nil {
			if string(old) != string(raw) {
				return "", fmt.Errorf("pool host configuration requires maintenance")
			}
		} else if os.IsNotExist(e) {
			if e = os.WriteFile(path, raw, 0444); e != nil {
				return "", e
			}
		} else {
			return "", e
		}
		relay := sharednode.NewRelay(client, b.PhysicalInstanceId)
		relay.Admission = admission
		relay.VerifyPeer = func(call context.Context, pid int, start string) (string, error) {
			return docker.VerifyPoolPeer(call, b.PhysicalInstanceId, pid, start)
		}
		if _, err = relay.Start(ctx, filepath.Join(directory, "shared-node.sock"), uid); err != nil {
			return "", err
		}
		started[b.PhysicalInstanceId] = directory
		return directory, nil
	}
	pools.Supervisor = docker
	manager = &runtime.PersistentPools{Pools: pools, Driver: docker,
		UIDs: storage.NewUIDTable(cfg.AgentDir()), Template: template, Profile: profile, Ports: ports,
		Resources:     &runtime.PoolMaterializer{Layout: pools.Layout, Source: client, Unified: runtime.NewUnifiedManager(&storage.Layout{InstancesDir: cfg.InstancesDir()}, docker, nil, cfg.AgentDir())},
		ConfigureHost: configure,
		Publish: func(pool string, endpoint runtime.Endpoint) {
			upstream, err := url.Parse(endpoint.URL)
			if err == nil {
				registry.Put(&ingress.Instance{ID: pool, Shared: true, Upstream: upstream})
			}
		},
		Unpublish: registry.Delete,
	}
	manager.Memory = func(ctx context.Context, pool string) (*nodev1.SharedDshPoolMemorySample, error) {
		sample, err := docker.PoolMemory(ctx, pool)
		if err == nil {
			sample.TenantId = client.Settings.TenantID
		}
		return sample, err
	}
	return manager, nil
}
