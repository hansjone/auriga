package observer

import (
	"sync"
	"time"
)

// ActivityTracker records CPU samples and reports recent activity (5 minutes).
type ActivityTracker struct {
	mu      sync.Mutex
	window  time.Duration
	clock   Clock
	samples map[string][]cpuSample
}

type cpuSample struct {
	at        time.Time
	cpuMillis int64
}

// NewActivityTracker creates a tracker with a 5-minute window.
func NewActivityTracker(clock Clock) *ActivityTracker {
	if clock == nil {
		clock = realClock{}
	}
	return &ActivityTracker{
		window:  5 * time.Minute,
		clock:   clock,
		samples: make(map[string][]cpuSample),
	}
}

// Observe records a CPU usage sample (cpu millis used in the last interval).
func (a *ActivityTracker) Observe(instanceID string, cpuMillis int64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	now := a.clock.Now()
	cut := now.Add(-a.window)
	s := append(a.samples[instanceID], cpuSample{at: now, cpuMillis: cpuMillis})
	j := 0
	for _, x := range s {
		if x.at.After(cut) {
			s[j] = x
			j++
		}
	}
	a.samples[instanceID] = s[:j]
}

// RecentCPUActive is true if any sample in the window had meaningful CPU.
func (a *ActivityTracker) RecentCPUActive(instanceID string, thresholdMillis int64) bool {
	if thresholdMillis <= 0 {
		thresholdMillis = 50 // ~1% of one core over a few seconds
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	now := a.clock.Now()
	cut := now.Add(-a.window)
	for _, x := range a.samples[instanceID] {
		if x.at.After(cut) && x.cpuMillis >= thresholdMillis {
			return true
		}
	}
	return false
}
