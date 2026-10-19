package runtime

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hansjone/auriga/infra/agent/internal/artifacts"
	"github.com/hansjone/auriga/infra/agent/internal/policy"
	"github.com/hansjone/auriga/infra/agent/internal/renderer"
	"github.com/hansjone/auriga/infra/agent/internal/storage"
	nodev1 "github.com/hansjone/auriga/infra/gen/auriga/node/v1"
)

// SpecSource loads the full desired InstanceSpec (typically state.Store).
type SpecSource interface {
	GetDesired(instanceID string) (*nodev1.InstanceSpec, error)
}

// Preparing wraps an inner Driver and performs artifact fetch, directory
// setup, and config render before Ensure.
type Preparing struct {
	Inner          Driver
	Specs          SpecSource
	Artifacts      *artifacts.Store
	Layout         *storage.Layout
	UIDs           *storage.UIDTable
	Policy         policy.Policy
	SealingPrivate [32]byte
	PlatformURL    string
	Chown          func(path string, uid, gid int) error
}

func (p *Preparing) Name() string {
	if p.Inner == nil {
		return "preparing"
	}
	return p.Inner.Name()
}

func (p *Preparing) Capabilities() Caps {
	if p.Inner == nil {
		return Caps{}
	}
	return p.Inner.Capabilities()
}

func (p *Preparing) Ensure(ctx context.Context, s *Spec, r *Rendered) error {
	if s == nil {
		return fmt.Errorf("preparing: nil spec")
	}
	if err := storage.ValidateInstanceID(s.InstanceID); err != nil {
		return err
	}
	full, err := p.Specs.GetDesired(s.InstanceID)
	if err != nil {
		return err
	}
	if full == nil {
		return p.Inner.Ensure(ctx, s, r)
	}
	if err := storage.ValidateInstanceID(full.InstanceId); err != nil {
		return err
	}

	uid := s.HostUID
	if p.UIDs != nil {
		uid, err = p.UIDs.Allocate(full.InstanceId)
		if err != nil {
			return err
		}
	}
	if uid < 200000 {
		return fmt.Errorf("preparing: missing uid allocation")
	}

	if p.Layout != nil {
		if p.Chown != nil {
			p.Layout.Chown = p.Chown
		}
		paths, err := p.Layout.Ensure(full.InstanceId, uid, uid)
		if err != nil {
			return err
		}
		wantSchema := uint32(0)
		if full.GetStorage() != nil {
			wantSchema = full.Storage.SchemaVersion
		}
		if wantSchema > 0 {
			if err := p.Layout.Migrate(full.InstanceId, wantSchema); err != nil {
				return err
			}
		}

		if p.Artifacts != nil {
			for _, pl := range full.Plugins {
				if pl == nil || pl.Url == "" || pl.Digest == "" {
					continue
				}
				if _, err := p.Artifacts.Ensure(ctx, full.InstanceId, pl.Url, pl.Digest, 0); err != nil {
					return fmt.Errorf("plugin %s: %w", pl.Name, err)
				}
			}
			if dsh := full.GetDsh(); dsh != nil && dsh.Url != "" && dsh.Digest != "" && dsh.Image == "" {
				if _, err := p.Artifacts.Ensure(ctx, full.InstanceId, dsh.Url, dsh.Digest, 0); err != nil {
					return fmt.Errorf("dsh runtime: %w", err)
				}
			}
		}

		rend, err := renderer.Render(full, renderer.Options{
			MetaDir:        paths.Meta,
			HomeDir:        paths.Home,
			PlatformURL:    p.PlatformURL,
			SealingPrivate: p.SealingPrivate,
			OwnerUID:       uid,
			OwnerGID:       uid,
			Chown:          p.Chown,
		})
		if err != nil {
			return err
		}
		if r == nil {
			r = &Rendered{}
		}
		r.Digest = rend.Digest

		env, err := loadEnvFile(rend.EnvFile)
		if err != nil {
			return err
		}
		s.HomeBind = paths.Home
		s.MetaBind = paths.Meta
		s.HostUID = uid
		s.Generation = full.Generation
		s.Labels = full.Labels
		if full.GetResources() != nil {
			s.CPUMillis = full.Resources.CpuMillis
			s.MemoryMB = full.Resources.MemoryMb
			s.PIDs = full.Resources.Pids
		}
		cpu, mem, pids := p.Policy.ClampResources(s.CPUMillis, s.MemoryMB, s.PIDs)
		s.CPUMillis, s.MemoryMB, s.PIDs = cpu, mem, pids
		if full.GetProfile() != nil {
			s.ListenPort = full.Profile.ListenPort
		}
		if full.GetDsh() != nil {
			s.Image = full.Dsh.Image
		}
		s.Env = env
	}
	return p.Inner.Ensure(ctx, s, r)
}

func (p *Preparing) Start(ctx context.Context, id string) error {
	return p.Inner.Start(ctx, id)
}
func (p *Preparing) Stop(ctx context.Context, id string, grace time.Duration) error {
	return p.Inner.Stop(ctx, id, grace)
}
func (p *Preparing) Remove(ctx context.Context, id string) error {
	if err := p.Inner.Remove(ctx, id); err != nil {
		return err
	}
	if p.Artifacts != nil {
		_ = p.Artifacts.ReleaseAll(id)
	}
	if p.UIDs != nil {
		_ = p.UIDs.Release(id)
	}
	if p.Layout != nil {
		_ = p.Layout.Remove(id)
	}
	return nil
}
func (p *Preparing) Inspect(ctx context.Context, id string) (Status, error) {
	return p.Inner.Inspect(ctx, id)
}
func (p *Preparing) Stats(ctx context.Context, id string) (Usage, error) {
	return p.Inner.Stats(ctx, id)
}
func (p *Preparing) Endpoint(id string) (Endpoint, error) { return p.Inner.Endpoint(id) }
func (p *Preparing) List(ctx context.Context) ([]Status, error) {
	return p.Inner.List(ctx)
}

func loadEnvFile(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	out := map[string]string{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		out[k] = v
	}
	return out, sc.Err()
}

// SelectDriver picks a driver by name: "docker" (default) or "fake".
func SelectDriver(name string, dockerCfg DockerConfig) (Driver, error) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "", "docker":
		return NewDocker(dockerCfg)
	case "fake":
		return NewFake(), nil
	default:
		return nil, fmt.Errorf("unknown driver %q (want docker|fake)", name)
	}
}

// PrimaryDriverName returns the first configured driver, defaulting to docker.
func PrimaryDriverName(drivers []string) string {
	for _, d := range drivers {
		d = strings.ToLower(strings.TrimSpace(d))
		if d != "" {
			return d
		}
	}
	return "docker"
}

// InstanceHome is a helper for tests.
func InstanceHome(instancesDir, id string) string {
	return filepath.Join(instancesDir, id, "home")
}
