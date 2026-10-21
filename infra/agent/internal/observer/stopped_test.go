package observer

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	nodev1 "github.com/hansjone/auriga/infra/gen/auriga/node/v1"
)

type stoppedReporter struct {
	captureReporter
	usage *nodev1.NodeUsage
}

func (r *stoppedReporter) ReportUsage(usage *nodev1.NodeUsage) { r.usage = usage }

func TestStoppedTargetsDoNotProbeOrPublishCPUAndForgetPriorActivity(t *testing.T) {
	for _, process := range []ProcessStatus{ProcessStopped, ProcessFailed} {
		calls := 0
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { calls++ }))
		base, _ := url.Parse(server.URL)
		result := ProbeHealth(context.Background(), server.Client(), InstanceTarget{ID: "stop-test", BaseURL: base, Process: process})
		server.Close()
		if result.Ready || result.HTTPOK || calls != 0 {
			t.Fatal("stopped process received HTTP probe")
		}
		clock := &MutableClock{T: time.Now()}
		collector := &cumulativeCollector{cpu: 1000}
		source := &SliceTargets{}
		source.Set([]InstanceTarget{{ID: "stop-test", Process: ProcessRunning}})
		reporter := &stoppedReporter{}
		o := New(Config{Clock: clock, Source: source, Collector: collector, Reporter: reporter})
		o.sampleUsage()
		collector.cpu = 1100
		o.sampleUsage()
		if !o.cfg.Activity.RecentCPUActive("stop-test", 50) {
			t.Fatal("running CPU fixture inactive")
		}
		source.Set([]InstanceTarget{{ID: "stop-test", Process: process}})
		o.sampleUsage()
		if len(reporter.usage.Instances) != 0 || len(o.lastCPU) != 0 || o.cfg.Activity.RecentCPUActive("stop-test", 50) {
			t.Fatal("stopped target published fresh CPU or retained activity")
		}
		source.Set([]InstanceTarget{{ID: "stop-test", Process: ProcessRunning}})
		collector.cpu = 5000
		o.sampleUsage()
		if reporter.usage.Instances[0].RecentCpuActive {
			t.Fatal("old CPU kept restarted process active")
		}
	}
}
