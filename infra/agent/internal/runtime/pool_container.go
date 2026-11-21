package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"

	"github.com/hansjone/auriga/infra/agent/internal/renderer"
	"github.com/hansjone/auriga/infra/agent/internal/storage"
)

type PoolContainer struct {
	Volume, Device, Members, Skills, Meta, Relay, ProfileDigest string
	Mode, DedicatedPrepared, DedicatedCache                     string
}

func (d *Docker) poolContainerPath(id string) (string, error) {
	return (&storage.PoolLayout{Root: filepath.Join(d.DataRoot, "pools")}).Path(id, "control", "container-state")
}

func (d *Docker) ensurePoolVolume(ctx context.Context, id string, pool *PoolContainer, create bool) error {
	if pool.Volume == "" {
		return nil
	}
	if pool.Volume != "auriga-pool-"+id || pool.Device == "" {
		return fmt.Errorf("invalid pool volume identity")
	}
	path := "/volumes/" + url.PathEscape(pool.Volume)
	raw, status, err := d.Client.doJSON(ctx, http.MethodGet, path, nil, http.StatusOK, http.StatusNotFound)
	if err != nil {
		return err
	}
	options := map[string]string{"type": "ext4", "device": pool.Device, "o": "nodev,nosuid,prjquota"}
	if status == http.StatusNotFound {
		if !create {
			return fmt.Errorf("recorded pool volume is missing")
		}
		_, _, err = d.Client.doJSON(ctx, http.MethodPost, "/volumes/create", map[string]any{
			"Name": pool.Volume, "Driver": "local", "DriverOpts": options,
			"Labels": map[string]string{labelManaged: "true", "auriga.pool": id},
		}, http.StatusCreated)
		if err != nil {
			return err
		}
		raw, _, err = d.Client.doJSON(ctx, http.MethodGet, path, nil, http.StatusOK)
		if err != nil {
			return err
		}
	}
	var actual struct {
		Name, Driver    string
		Options, Labels map[string]string
	}
	if err := json.Unmarshal(raw, &actual); err != nil {
		return err
	}
	if actual.Name != pool.Volume || actual.Driver != "local" || actual.Labels[labelManaged] != "true" || actual.Labels["auriga.pool"] != id || len(actual.Options) != len(options) {
		return fmt.Errorf("pool volume ownership mismatch")
	}
	for key, value := range options {
		if actual.Options[key] != value {
			return fmt.Errorf("pool volume device or options changed")
		}
	}
	return nil
}

func (d *Docker) configurePool(ctx context.Context, spec *Spec, body map[string]any) error {
	p := spec.Pool
	if p.Meta != spec.MetaBind || p.ProfileDigest == "" || !filepath.IsAbs(p.Members) || !filepath.IsAbs(p.Skills) {
		return fmt.Errorf("incomplete pool container configuration")
	}
	if err := d.ensurePoolVolume(ctx, spec.InstanceID, p, true); err != nil {
		return err
	}
	host := body["HostConfig"].(map[string]any)
	mounts := host["Mounts"].([]map[string]any)
	memberTarget := "/var/lib/auriga/pools/" + spec.InstanceID + "/members"
	member := map[string]any{"Type": "bind", "Source": p.Members, "Target": memberTarget, "ReadOnly": false}
	if p.Volume != "" {
		member["Type"], member["Source"] = "volume", p.Volume
		mounts[0] = map[string]any{"Type": "volume", "Source": p.Volume, "Target": "/home/dsh", "ReadOnly": false,
			"VolumeOptions": map[string]any{"NoCopy": true}}
		member["VolumeOptions"] = map[string]any{"NoCopy": true}
	}
	host["Mounts"] = append(mounts, member, map[string]any{"Type": "bind", "Source": p.Skills,
		"Target": "/run/auriga/skills-active/current", "ReadOnly": true, "RW": false})
	cacheMount := map[string]any{"Type": member["Type"], "Source": member["Source"], "Target": "/var/lib/auriga/pools/" + spec.InstanceID + "/host", "ReadOnly": false}
	if p.Volume != "" {
		cacheMount["VolumeOptions"] = map[string]any{"NoCopy": true}
	}
	host["Mounts"] = append(host["Mounts"].([]map[string]any), cacheMount)
	if p.Relay != "" {
		host["Mounts"] = append(host["Mounts"].([]map[string]any), map[string]any{"Type": "bind", "Source": p.Relay, "Target": "/var/lib/auriga/pools/" + spec.InstanceID + "/control", "ReadOnly": true})
	}
	launcher := "/run/auriga/pool-launcher.mjs"
	if p.Mode == "dedicated" {
		if !filepath.IsAbs(p.DedicatedPrepared) || !filepath.IsAbs(p.DedicatedCache) || p.Relay == "" {
			return fmt.Errorf("dedicated prepared output and verified cache required")
		}
		host["Mounts"] = append(host["Mounts"].([]map[string]any),
			map[string]any{"Type": "bind", "Source": p.DedicatedPrepared, "Target": "/run/auriga/dedicated-prepared", "ReadOnly": true},
			map[string]any{"Type": "bind", "Source": p.DedicatedCache, "Target": "/run/auriga/dedicated-resources", "ReadOnly": true})
		launcher = "/run/auriga/dedicated-launcher.mjs"
		// Ensure has already verified and removed the exact old stopped
		// container using its OLD expectations. Only now replace this record.
		path, err := d.poolContainerPath(spec.InstanceID)
		if err != nil {
			return err
		}
		if err = unifiedAtomic(path, p); err != nil {
			return err
		}
	} else if p.Mode != "" && p.Mode != "shared" || p.DedicatedPrepared != "" || p.DedicatedCache != "" {
		return fmt.Errorf("invalid pool mode mounts")
	}
	host["RestartPolicy"] = map[string]any{"Name": "no"}
	body["Entrypoint"] = []string{"node", renderer.ContainerResourceSupervisor}
	body["Cmd"] = []string{renderer.ContainerResourcePolicy, "node", launcher}
	labels := body["Labels"].(map[string]string)
	labels["auriga.shared-pool"], labels["auriga.shared-profile"] = "true", p.ProfileDigest
	if p.Mode == "dedicated" {
		labels["auriga.pool-mode"] = "dedicated"
	}
	return nil
}

// Immutable node-owned mount/config expectation survives an agent process restart.
func (d *Docker) verifyPoolContainer(ctx context.Context, id string, inspect map[string]any) error {
	path, err := d.poolContainerPath(id)
	if err != nil {
		return err
	}
	cfg, _ := inspect["Config"].(map[string]any)
	labels, _ := cfg["Labels"].(map[string]any)
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) && labels["auriga.shared-pool"] == nil {
		return nil
	}
	if err != nil {
		return err
	}
	var p PoolContainer
	if err := json.Unmarshal(raw, &p); err != nil {
		return err
	}
	launcher := "/run/auriga/pool-launcher.mjs"
	if p.Mode == "dedicated" {
		if labels["auriga.pool-mode"] != "dedicated" || !filepath.IsAbs(p.DedicatedPrepared) || !filepath.IsAbs(p.DedicatedCache) {
			return fmt.Errorf("dedicated runtime identity mismatch")
		}
		launcher = "/run/auriga/dedicated-launcher.mjs"
	} else if p.DedicatedPrepared != "" || p.DedicatedCache != "" || labels["auriga.pool-mode"] != nil {
		return fmt.Errorf("shared runtime has dedicated output")
	}
	host, _ := inspect["HostConfig"].(map[string]any)
	capAdd, capShape := host["CapAdd"].([]any)
	if host["CapAdd"] != nil && !capShape {
		return fmt.Errorf("invalid shared container capabilities")
	}
	restart, _ := host["RestartPolicy"].(map[string]any)
	if labels["auriga.shared-pool"] != "true" || labels["auriga.shared-profile"] != p.ProfileDigest || restart["Name"] != "no" ||
		host["ReadonlyRootfs"] != true || host["Privileged"] != false || len(capAdd) != 0 ||
		!containsString(host["CapDrop"], "ALL") || !containsString(host["SecurityOpt"], "no-new-privileges:true") ||
		!sameStrings(cfg["Entrypoint"], []string{"node", renderer.ContainerResourceSupervisor}) ||
		!sameStrings(cfg["Cmd"], []string{renderer.ContainerResourcePolicy, "node", launcher}) {
		return fmt.Errorf("shared process profile identity mismatch")
	}
	mounts, _ := inspect["Mounts"].([]any)
	count := 5
	if p.Relay != "" {
		count++
	}
	if p.Mode == "dedicated" {
		count += 2
	}
	if len(mounts) != count {
		return fmt.Errorf("shared process mounts unavailable")
	}
	expected := map[string]struct {
		source, kind string
		rw           bool
	}{
		"/home/dsh": {p.Members, "bind", true},
		"/var/lib/auriga/pools/" + id + "/members": {p.Members, "bind", true},
		"/var/lib/auriga/pools/" + id + "/host":    {p.Members, "bind", true},
		"/run/auriga":                              {p.Meta, "bind", false},
		"/run/auriga/skills-active/current":        {p.Skills, "bind", false},
	}
	if p.Relay != "" {
		expected["/var/lib/auriga/pools/"+id+"/control"] = struct {
			source, kind string
			rw           bool
		}{p.Relay, "bind", false}
	}
	if p.Mode == "dedicated" {
		expected["/run/auriga/dedicated-prepared"] = struct {
			source, kind string
			rw           bool
		}{p.DedicatedPrepared, "bind", false}
		expected["/run/auriga/dedicated-resources"] = struct {
			source, kind string
			rw           bool
		}{p.DedicatedCache, "bind", false}
	}
	if p.Volume != "" {
		expected["/home/dsh"] = struct {
			source, kind string
			rw           bool
		}{p.Volume, "volume", true}
		expected["/var/lib/auriga/pools/"+id+"/members"] = struct {
			source, kind string
			rw           bool
		}{p.Volume, "volume", true}
		expected["/var/lib/auriga/pools/"+id+"/host"] = struct {
			source, kind string
			rw           bool
		}{p.Volume, "volume", true}
		if err := d.ensurePoolVolume(ctx, id, &p, false); err != nil {
			return err
		}
	}
	for _, item := range mounts {
		mount, _ := item.(map[string]any)
		dst, _ := mount["Destination"].(string)
		want, ok := expected[dst]
		source, _ := mount["Source"].(string)
		if want.kind == "volume" {
			source, _ = mount["Name"].(string)
		}
		if !ok || mount["Type"] != want.kind || source != want.source || mount["RW"] != want.rw {
			return fmt.Errorf("shared mount provenance mismatch")
		}
		delete(expected, dst)
	}
	if len(expected) != 0 {
		return fmt.Errorf("shared mount missing")
	}
	return nil
}
