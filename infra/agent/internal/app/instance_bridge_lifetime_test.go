package app

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/hansjone/auriga/infra/agent/internal/config"
	"github.com/hansjone/auriga/infra/agent/internal/ingress"
	"github.com/hansjone/auriga/infra/agent/internal/observer"
	nodev1 "github.com/hansjone/auriga/infra/gen/auriga/node/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type stepCPUCollector struct {
	entered chan struct{}
	values  chan int64
	done    chan struct{}
	cpu     int64
}

func (c *stepCPUCollector) CollectNode() (observer.NodeResources, error) {
	select {
	case c.entered <- struct{}{}:
	case <-c.done:
		return observer.NodeResources{}, nil
	}
	select {
	case c.cpu = <-c.values:
	case <-c.done:
	}
	return observer.NodeResources{}, nil
}
func (c *stepCPUCollector) CollectInstance(observer.InstanceTarget) (observer.InstanceResources, error) {
	return observer.InstanceResources{CPUMillis: c.cpu}, nil
}

type stepUsageReporter struct{ usages chan *nodev1.NodeUsage }

func (r stepUsageReporter) ReportUsage(usage *nodev1.NodeUsage) { r.usages <- usage }
func (stepUsageReporter) ReportReady(string, bool)              {}
func (stepUsageReporter) ReportWake(*nodev1.WakeSchedule)       {}

func TestBridgeFastRestartInvalidatesCPUAndPreservesSameProcessUpdates(t *testing.T) {
	for _, counter := range []int64{10, 5000} {
		t.Run(fmt.Sprintf("%dms", counter), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(`{"status":"ok","ready":true,"protocol":1,"pluginVersion":"test"}`))
			}))
			t.Cleanup(server.Close)
			targets := &observer.SliceTargets{}
			bridge := newInstanceBridge(config.Config{DataRoot: t.TempDir()}, &endpointDriver{endpoint: server.URL}, ingress.NewMapRegistry(), targets)
			firstStart, secondStart := timestamppb.New(time.Now().Add(-time.Hour)), timestamppb.Now()
			syncStatus := func(phase nodev1.InstancePhase, started *timestamppb.Timestamp) {
				bridge.Sync(&nodev1.InstanceStatus{InstanceId: "cpu-lifetime", Phase: phase, Ready: true, StartedAt: started})
			}
			syncStatus(nodev1.InstancePhase_INSTANCE_PHASE_RUNNING, firstStart)
			collector := &stepCPUCollector{entered: make(chan struct{}), values: make(chan int64), done: make(chan struct{})}
			reporter := stepUsageReporter{usages: make(chan *nodev1.NodeUsage, 8)}
			obs := observer.New(observer.Config{Source: targets, Collector: collector, Reporter: reporter, UsageEvery: time.Millisecond, HealthEvery: time.Hour, WatchPoll: time.Hour})
			obs.Start(context.Background())
			t.Cleanup(func() { close(collector.done); obs.Stop() })
			poll := func(cpu int64, active bool, transition func()) {
				t.Helper()
				select {
				case <-collector.entered:
				case <-time.After(time.Second):
					t.Fatal("no usage poll")
				}
				if transition != nil {
					transition()
				}
				collector.values <- cpu
				select {
				case usage := <-reporter.usages:
					if len(usage.Instances) != 1 || usage.Instances[0].RecentCpuActive != active {
						t.Fatalf("cpu %d, want active=%v, got %v", cpu, active, usage)
					}
				case <-time.After(time.Second):
					t.Fatal("usage report missing")
				}
			}
			poll(1000, false, nil)
			poll(1100, true, nil)
			poll(counter, false, func() {
				// Both transitions complete before the source is read by the next poll.
				syncStatus(nodev1.InstancePhase_INSTANCE_PHASE_STOPPED, firstStart)
				syncStatus(nodev1.InstancePhase_INSTANCE_PHASE_RUNNING, secondStart)
			})
			poll(counter+60, true, nil)
			poll(counter+61, true, func() { syncStatus(nodev1.InstancePhase_INSTANCE_PHASE_RUNNING, secondStart) })
			poll(counter+62, true, func() { syncStatus(nodev1.InstancePhase_INSTANCE_PHASE_PENDING_RESTART, secondStart) })
		})
	}
}
