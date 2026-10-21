package observer

import (
	"fmt"
	"testing"
	"time"
)

func TestUsageRapidRestartForgetsCPUWithoutStoppedSample(t *testing.T) {
	for _, initial := range []int64{10, 5000} {
		for _, transition := range []string{"stop-start", "started-at"} {
			t.Run(fmt.Sprintf("%dms/%s", initial, transition), func(t *testing.T) {
				source := &SliceTargets{}
				target := InstanceTarget{ID: "rapid-test", Process: ProcessRunning, ProcessStartedAt: time.Unix(1, 0)}
				source.Set([]InstanceTarget{target})
				collector, reporter := &cumulativeCollector{cpu: 1000}, &stoppedReporter{}
				o := New(Config{Source: source, Collector: collector, Reporter: reporter})
				sample := func(cpu int64, active bool) {
					t.Helper()
					collector.cpu = cpu
					o.sampleUsage()
					if len(reporter.usage.Instances) != 1 || reporter.usage.Instances[0].RecentCpuActive != active {
						t.Fatalf("cpu=%d wanted activity %v, got %v", cpu, active, reporter.usage)
					}
				}
				sample(1000, false)
				sample(1100, true)
				// Repeated publication and unrelated root metadata describe the same process.
				target.RootDir = "metadata-only"
				source.Set([]InstanceTarget{target})
				sample(1101, true)
				if transition == "stop-start" {
					target.Process = ProcessStopped
					source.Set([]InstanceTarget{target})
					target.Process = ProcessRunning
				} else {
					target.ProcessStartedAt = time.Unix(2, 0)
				}
				source.Set([]InstanceTarget{target})
				// No sample is inserted between the actual stop and start.
				sample(initial, false)
				sample(initial+60, true)
			})
		}
	}
}
