package runtime

import (
	"bufio"
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
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
	Log            *slog.Logger
	// ExtraEnvKeys are the Profile.Env keys a spec may set besides the
	// platform keys; nil means DefaultExtraEnvKeys.
	ExtraEnvKeys []string
	// NewTicker drives the periodic GC; nil uses time.NewTicker. Tests pass a
	// manual clock.
	NewTicker func(d time.Duration) (<-chan time.Time, func())

	// gcMu keeps artifact GC out of the fetch-to-acquire window: prepares
	// hold it shared while fetching, GC holds it exclusively.
	gcMu sync.RWMutex
}

func (p *Preparing) log() *slog.Logger {
	if p.Log == nil {
		return slog.Default()
	}
	return p.Log
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
			p.gcMu.RLock()
			err := p.fetchArtifacts(ctx, full)
			p.gcMu.RUnlock()
			if err != nil {
				return err
			}
			p.tryCollectGarbage(ctx)
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

		rawEnv, err := loadEnvFile(rend.EnvFile)
		if err != nil {
			return err
		}
		env, err := ContainerEnv(rawEnv, full.GetProfile().GetEnv(), p.extraEnvKeys(), p.log().With("instance_id", full.InstanceId))
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
	p.log().Info("pulling image and creating container", "instance_id", s.InstanceID)
	return p.Inner.Ensure(ctx, s, r)
}

func (p *Preparing) extraEnvKeys() []string {
	if p.ExtraEnvKeys == nil {
		return DefaultExtraEnvKeys
	}
	return p.ExtraEnvKeys
}

// fetchArtifacts downloads and pins every artifact the spec names, then drops
// this instance's holds on digests it no longer uses. Caller holds gcMu.RLock.
func (p *Preparing) fetchArtifacts(ctx context.Context, full *nodev1.InstanceSpec) error {
	var wantDigests []string
	for _, pl := range full.Plugins {
		if pl == nil || pl.Url == "" || pl.Digest == "" {
			continue
		}
		p.log().Info("fetching plugin artifact", "instance_id", full.InstanceId, "plugin", pl.Name)
		if _, err := p.Artifacts.Ensure(ctx, full.InstanceId, pl.Url, pl.Digest, 0); err != nil {
			return fmt.Errorf("plugin %s: %w", pl.Name, err)
		}
		wantDigests = append(wantDigests, pl.Digest)
	}
	if dsh := full.GetDsh(); dsh != nil && dsh.Url != "" && dsh.Digest != "" && dsh.Image == "" {
		p.log().Info("fetching dsh runtime artifact", "instance_id", full.InstanceId)
		if _, err := p.Artifacts.Ensure(ctx, full.InstanceId, dsh.Url, dsh.Digest, 0); err != nil {
			return fmt.Errorf("dsh runtime: %w", err)
		}
		wantDigests = append(wantDigests, dsh.Digest)
	}
	if err := p.Artifacts.SyncHolders(full.InstanceId, wantDigests); err != nil {
		return fmt.Errorf("artifacts sync: %w", err)
	}
	return nil
}

// specDigests lists the artifact digests a spec references (valid ones only).
func specDigests(spec *nodev1.InstanceSpec) []string {
	if spec == nil {
		return nil
	}
	var out []string
	add := func(d string) {
		if d == "" {
			return
		}
		if _, err := artifacts.NormalizeDigest(d); err == nil {
			out = append(out, d)
		}
	}
	for _, pl := range spec.Plugins {
		if pl != nil {
			add(pl.Digest)
		}
	}
	if dsh := spec.GetDsh(); dsh != nil {
		add(dsh.Digest)
	}
	return out
}

// desiredLister is implemented by state.Store.
type desiredLister interface {
	ListDesiredIDs() ([]string, error)
}

// liveDigests collects every digest named by a desired spec or by a spec of an
// instance the runtime still has. Any listing error aborts GC (fail closed).
func (p *Preparing) liveDigests(ctx context.Context) ([]string, error) {
	ids := map[string]struct{}{}
	if p.Inner != nil {
		list, err := p.Inner.List(ctx)
		if err != nil {
			return nil, fmt.Errorf("list instances: %w", err)
		}
		for _, st := range list {
			ids[st.InstanceID] = struct{}{}
		}
	}
	if l, ok := p.Specs.(desiredLister); ok {
		desired, err := l.ListDesiredIDs()
		if err != nil {
			return nil, fmt.Errorf("list desired: %w", err)
		}
		for _, id := range desired {
			ids[id] = struct{}{}
		}
	}
	var keep []string
	for id := range ids {
		spec, err := p.Specs.GetDesired(id)
		if err != nil {
			return nil, fmt.Errorf("desired %s: %w", id, err)
		}
		keep = append(keep, specDigests(spec)...)
	}
	return keep, nil
}

// CollectGarbage removes unreferenced cached artifacts, never touching a
// digest that any desired or present instance names. It waits for in-flight
// artifact fetches and blocks new ones while it runs.
func (p *Preparing) CollectGarbage(ctx context.Context) (int, error) {
	if p.Artifacts == nil {
		return 0, nil
	}
	p.gcMu.Lock()
	defer p.gcMu.Unlock()
	return p.collectLocked(ctx)
}

// tryCollectGarbage runs GC after a prepare unless a fetch or GC is in flight
// (the periodic loop catches up later).
func (p *Preparing) tryCollectGarbage(ctx context.Context) {
	if !p.gcMu.TryLock() {
		return
	}
	defer p.gcMu.Unlock()
	if n, err := p.collectLocked(ctx); err != nil {
		p.log().Warn("artifact gc failed", "error", err)
	} else if n > 0 {
		p.log().Info("artifact gc", "removed", n)
	}
}

func (p *Preparing) collectLocked(ctx context.Context) (int, error) {
	keep, err := p.liveDigests(ctx)
	if err != nil {
		return 0, fmt.Errorf("artifact gc skipped: %w", err)
	}
	return p.Artifacts.GCKeep(false, keep)
}

// StartGC runs CollectGarbage every interval until stop is called or ctx
// ends. interval <= 0 (or no artifact store) disables it. stop waits for the
// loop to exit and is safe to call more than once.
func (p *Preparing) StartGC(ctx context.Context, interval time.Duration) (stop func()) {
	if interval <= 0 || p.Artifacts == nil {
		return func() {}
	}
	newTicker := p.NewTicker
	if newTicker == nil {
		newTicker = func(d time.Duration) (<-chan time.Time, func()) {
			t := time.NewTicker(d)
			return t.C, t.Stop
		}
	}
	tick, stopTicker := newTicker(interval)
	quit := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer stopTicker()
		for {
			select {
			case <-ctx.Done():
				return
			case <-quit:
				return
			case <-tick:
				n, err := p.CollectGarbage(ctx)
				if err != nil {
					p.log().Warn("periodic artifact gc failed", "error", err)
				} else if n > 0 {
					p.log().Info("periodic artifact gc", "removed", n)
				}
			}
		}
	}()
	var once sync.Once
	return func() {
		once.Do(func() { close(quit) })
		<-done
	}
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
