package observer

import (
	"context"
	"log/slog"
	"net/http"
	"sync"
	"time"

	nodev1 "github.com/hansjone/auriga/infra/gen/auriga/node/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Reporter receives observation updates (wired to NodeControl session by app).
type Reporter interface {
	ReportWake(w *nodev1.WakeSchedule)
	ReportUsage(u *nodev1.NodeUsage)
	ReportReady(instanceID string, ready bool)
}

// LogReporter logs reports (default when session hooks are not wired).
type LogReporter struct{ Log *slog.Logger }

func (r LogReporter) ReportWake(w *nodev1.WakeSchedule) {
	if r.Log != nil {
		r.Log.Info("wake schedule", "instance", w.GetInstanceId(), "source", w.GetSource(), "next", w.GetNextWakeAt())
	}
}
func (r LogReporter) ReportUsage(u *nodev1.NodeUsage) {
	if r.Log != nil {
		r.Log.Debug("node usage", "instances", len(u.GetInstances()))
	}
}
func (r LogReporter) ReportReady(instanceID string, ready bool) {
	if r.Log != nil {
		r.Log.Info("instance ready", "instance", instanceID, "ready", ready)
	}
}

// TargetSource lists instances to observe.
type TargetSource interface {
	Targets() []InstanceTarget
}

// SliceTargets is a static/mutable target list for tests.
type SliceTargets struct {
	mu sync.Mutex
	t  []InstanceTarget
}

func (s *SliceTargets) Targets() []InstanceTarget {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]InstanceTarget, len(s.t))
	copy(out, s.t)
	return out
}

func (s *SliceTargets) Set(t []InstanceTarget) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.t = append([]InstanceTarget(nil), t...)
}

// Config configures the observer loop.
type Config struct {
	Source     TargetSource
	Reporter   Reporter
	Collector  ResourceCollector
	Activity   *ActivityTracker
	Clock      Clock
	Logger     *slog.Logger
	HTTPClient *http.Client
	// HealthEvery / UsageEvery / WatchPoll defaults: 15s / 15s / 1s.
	HealthEvery time.Duration
	UsageEvery  time.Duration
	WatchPoll   time.Duration
}

// Observer probes health, watches cron files, and samples resources.
type Observer struct {
	lastCPU map[string]int64
	cfg     Config
	cancel  context.CancelFunc
	wg      sync.WaitGroup
}

// New creates an Observer (not started).
func New(cfg Config) *Observer {
	if cfg.Clock == nil {
		cfg.Clock = realClock{}
	}
	if cfg.Collector == nil {
		cfg.Collector = DefaultCollector()
	}
	if cfg.Activity == nil {
		cfg.Activity = NewActivityTracker(cfg.Clock)
	}
	if cfg.Reporter == nil {
		cfg.Reporter = LogReporter{Log: cfg.Logger}
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: 3 * time.Second}
	}
	if cfg.HealthEvery <= 0 {
		cfg.HealthEvery = 15 * time.Second
	}
	if cfg.UsageEvery <= 0 {
		cfg.UsageEvery = 15 * time.Second
	}
	if cfg.WatchPoll <= 0 {
		cfg.WatchPoll = time.Second
	}
	return &Observer{cfg: cfg, lastCPU: make(map[string]int64)}
}

// Start runs background loops until Stop or ctx cancel.
func (o *Observer) Start(ctx context.Context) {
	ctx, o.cancel = context.WithCancel(ctx)
	o.wg.Add(3)
	go func() { defer o.wg.Done(); o.healthLoop(ctx) }()
	go func() { defer o.wg.Done(); o.usageLoop(ctx) }()
	go func() { defer o.wg.Done(); o.watchLoop(ctx) }()
}

// Stop cancels loops and waits.
func (o *Observer) Stop() {
	if o.cancel != nil {
		o.cancel()
	}
	o.wg.Wait()
}

func (o *Observer) healthLoop(ctx context.Context) {
	t := time.NewTicker(o.cfg.HealthEvery)
	defer t.Stop()
	o.probeAll(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			o.probeAll(ctx)
		}
	}
}

func (o *Observer) probeAll(ctx context.Context) {
	if o.cfg.Source == nil {
		return
	}
	for _, target := range o.cfg.Source.Targets() {
		res := ProbeHealth(ctx, o.cfg.HTTPClient, target)
		o.cfg.Reporter.ReportReady(res.InstanceID, res.Ready)
	}
}

func (o *Observer) usageLoop(ctx context.Context) {
	t := time.NewTicker(o.cfg.UsageEvery)
	defer t.Stop()
	o.sampleUsage()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			o.sampleUsage()
		}
	}
}

func (o *Observer) sampleUsage() {
	node, _ := o.cfg.Collector.CollectNode()
	usage := &nodev1.NodeUsage{
		SampledAt: timestamppb.New(o.cfg.Clock.Now()),
		Node: &nodev1.NodeResources{
			CpuMillisUsed: node.CPUMillisUsed,
			MemoryMbUsed:  node.MemoryMBUsed,
			DiskGbUsed:    node.DiskGBUsed,
			DiskGbFree:    node.DiskGBFree,
		},
	}
	if o.cfg.Source != nil {
		active := make(map[string]bool)
		for _, target := range o.cfg.Source.Targets() {
			active[target.ID] = true
			ir, err := o.cfg.Collector.CollectInstance(target)
			if err != nil {
				delete(o.lastCPU, target.ID)
				continue
			}
			delta := int64(0)
			if prev, ok := o.lastCPU[target.ID]; ok && ir.CPUMillis >= prev {
				delta = ir.CPUMillis - prev
			}
			o.lastCPU[target.ID] = ir.CPUMillis
			o.cfg.Activity.Observe(target.ID, delta)
			usage.Instances = append(usage.Instances, &nodev1.InstanceUsage{
				InstanceId:      target.ID,
				CpuMillis:       ir.CPUMillis,
				MemoryMb:        ir.MemoryMB,
				DiskMb:          ir.DiskMB,
				Pids:            ir.PIDs,
				NetRxBytes:      ir.NetRxBytes,
				NetTxBytes:      ir.NetTxBytes,
				RecentCpuActive: o.cfg.Activity.RecentCPUActive(target.ID, 50),
			})
		}
		for id := range o.lastCPU {
			if !active[id] {
				delete(o.lastCPU, id)
			}
		}
	}
	o.cfg.Reporter.ReportUsage(usage)
}

func (o *Observer) watchLoop(ctx context.Context) {
	t := time.NewTicker(o.cfg.WatchPoll)
	defer t.Stop()
	last := map[string]time.Time{}
	o.refreshWakes(last)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			o.refreshWakes(last)
		}
	}
}

func (o *Observer) refreshWakes(lastMod map[string]time.Time) {
	if o.cfg.Source == nil {
		return
	}
	now := o.cfg.Clock.Now()
	for _, target := range o.cfg.Source.Targets() {
		if target.RootDir == "" {
			continue
		}
		changed := scheduleFilesChanged(target.RootDir, target.ID, lastMod)
		if !changed {
			continue
		}
		info, err := NextWakeFromInstanceDir(target.RootDir, now)
		if err != nil {
			o.cfg.Logger.Warn("wake parse failed", "instance", target.ID)
			continue
		}
		w := &nodev1.WakeSchedule{
			InstanceId: target.ID,
			Source:     info.Source,
		}
		if info.NextWakeAt != nil {
			w.NextWakeAt = timestamppb.New(*info.NextWakeAt)
		}
		o.cfg.Reporter.ReportWake(w)
	}
}

// RefreshWakeNow forces a wake recalculation (tests / after file write).
func (o *Observer) RefreshWakeNow(instanceID, rootDir string) (*nodev1.WakeSchedule, error) {
	info, err := NextWakeFromInstanceDir(rootDir, o.cfg.Clock.Now())
	if err != nil {
		return nil, err
	}
	w := &nodev1.WakeSchedule{InstanceId: instanceID, Source: info.Source}
	if info.NextWakeAt != nil {
		w.NextWakeAt = timestamppb.New(*info.NextWakeAt)
	}
	o.cfg.Reporter.ReportWake(w)
	return w, nil
}
