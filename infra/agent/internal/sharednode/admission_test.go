package sharednode

import (
	"context"
	"encoding/json"
	"errors"
	nodev1 "github.com/hansjone/auriga/infra/gen/auriga/node/v1"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestOrdinaryBeginRequiresProductionAdmission(t *testing.T) {
	r, a := fixture(t)
	r.Admission = nil
	binding, _ := wire(a.binding)
	raw, _ := json.Marshal(map[string]any{"binding": binding, "taskId": testTask, "audience": "auriga.shared.enterprise", "scopes": []string{"task.execute"}})
	if _, err := r.invoke(t.Context(), "beginTask", raw); err == nil {
		t.Fatal("ordinary Begin reached IAM without an installed production budget/memory admission")
	}
}

func TestBeginRejectsSelfReportedMemoryAndTaskOwner(t *testing.T) {
	r, a := fixture(t)
	b, _ := wire(a.binding)
	for _, field := range []string{"memoryUsedBytes", "memoryLimitBytes", "ownerId", "memberId", "parentTaskId"} {
		input := map[string]any{"binding": b, "taskId": testTask, "scopes": []string{"task.execute"}, "audience": "auriga.shared.enterprise", field: 0}
		raw, _ := json.Marshal(input)
		req := httptest.NewRequest("POST", "/_auriga/shared-rpc/beginTask", strings.NewReader(string(raw)))
		req.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		r.ServeHTTP(response, req)
		if response.Code != 503 || !strings.Contains(response.Body.String(), `"code":"common.validation_failed"`) {
			t.Fatal("untrusted admission assertion accepted", field, response.Body.String())
		}
	}
}

// This unit boundary observes lifecycle ordering; the separate runtime HTTP
// combination uses actual SharedPools and the signed Engine sampling adapter.
type lifecycleAdmission struct {
	mu        sync.Mutex
	active    map[string]bool
	completed int
	onAdmit   func()
}

func (a *lifecycleAdmission) Admit(_ context.Context, _ *nodev1.SharedDshSessionBinding, id string) (bool, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.active == nil {
		a.active = map[string]bool{}
	}
	a.active[id] = true
	if a.onAdmit != nil {
		a.onAdmit()
	}
	return true, nil
}
func (a *lifecycleAdmission) Check(_ *nodev1.SharedDshSessionBinding, id string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.active[id] {
		return errors.New("not active")
	}
	return nil
}
func (a *lifecycleAdmission) Complete(_ *nodev1.SharedDshSessionBinding, id string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.active[id] {
		a.completed++
		delete(a.active, id)
	}
	return nil
}
func awaitAdmission(t *testing.T, channel <-chan struct{}) {
	t.Helper()
	select {
	case <-channel:
	case <-time.After(3 * time.Second):
		t.Fatal("lifecycle did not reach its boundary")
	}
}
func awaitAdmissionError(t *testing.T, channel <-chan error) error {
	t.Helper()
	select {
	case err := <-channel:
		return err
	case <-time.After(3 * time.Second):
		t.Fatal("lifecycle did not finish")
		return nil
	}
}

func TestBeginFinishAndCancellationCannotPublishLateHandle(t *testing.T) {
	for _, scenario := range []string{"finish", "cancel", "process-invalid"} {
		t.Run(scenario, func(t *testing.T) {
			r, a := fixture(t)
			gate := &lifecycleAdmission{}
			r.Admission = gate
			if scenario == "cancel" {
				a.finishMode.Store(1)
			}
			a.beginStarted, a.beginResume = make(chan struct{}, 1), make(chan struct{})
			t.Cleanup(func() {
				select {
				case <-a.beginResume:
				default:
					close(a.beginResume)
				}
			})
			var valid atomic.Bool
			valid.Store(true)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			ctx = context.WithValue(ctx, executionFenceKey{}, func() error {
				if !valid.Load() {
					return errors.New("shared.revoked")
				}
				return nil
			})
			b, _ := wire(a.binding)
			raw, _ := json.Marshal(map[string]any{"binding": b, "taskId": testTask, "audience": "auriga.shared.enterprise", "scopes": []string{"task.execute"}})
			done := make(chan error, 1)
			go func() { _, err := r.invoke(ctx, "beginTask", raw); done <- err }()
			awaitAdmission(t, a.beginStarted)
			var finished chan error
			if scenario == "finish" {
				finished = make(chan error, 1)
				go func() { finished <- r.finish(a.binding, testTask) }()
				timer := time.NewTimer(3 * time.Second)
				defer timer.Stop()
				ticker := time.NewTicker(time.Millisecond)
				defer ticker.Stop()
				for {
					r.mu.Lock()
					closing := r.tasks[testTask].closing
					r.mu.Unlock()
					if closing {
						break
					}
					select {
					case <-timer.C:
						t.Fatal("Finish did not fence in-flight Begin")
					case <-ticker.C:
					}
				}
			} else if scenario == "cancel" {
				cancel()
				if err := awaitAdmissionError(t, done); err == nil {
					t.Fatal("canceled unknown Begin returned a handle")
				}
				close(a.beginResume)
				r.RetryFinishes()
				r.mu.Lock()
				pending := r.tasks[testTask]
				tokens := len(r.tokens)
				r.mu.Unlock()
				gate.mu.Lock()
				active, completed := len(gate.active), gate.completed
				gate.mu.Unlock()
				if !pending.closing || !pending.uncertainBegin || tokens != 0 || active != 1 || completed != 0 {
					t.Fatal("ambiguous remote Begin freed its slot without tombstone evidence")
				}
				return
			} else {
				valid.Store(false)
			}
			close(a.beginResume)
			if err := awaitAdmissionError(t, done); err == nil {
				t.Fatal("late Begin published a capability")
			}
			if finished != nil {
				if err := awaitAdmissionError(t, finished); err != nil {
					t.Fatal(err)
				}
			}
			r.mu.Lock()
			tasks, tokens := len(r.tasks), len(r.tokens)
			r.mu.Unlock()
			gate.mu.Lock()
			active, completed := len(gate.active), gate.completed
			gate.mu.Unlock()
			if tasks != 0 || tokens != 0 || active != 0 || completed != 1 {
				t.Fatal("failed Begin leaked or double-released", tasks, tokens, active, completed)
			}
		})
	}
}

func TestNotDispatchedBeginKeepsReservationUntilAbsentTaskFence(t *testing.T) {
	r, a := fixture(t)
	gate := &lifecycleAdmission{onAdmit: func() {
		if err := os.WriteFile(r.Client.Settings.ServiceTokenFile, nil, 0600); err != nil {
			t.Error(err)
		}
	}}
	r.Admission = gate
	b, _ := wire(a.binding)
	raw, _ := json.Marshal(map[string]any{"binding": b, "taskId": testTask, "scopes": []string{"task.execute"}, "audience": "auriga.shared.enterprise"})
	if _, err := r.invoke(t.Context(), "beginTask", raw); err == nil {
		t.Fatal("missing service identity dispatched a task")
	}
	if len(r.tasks) != 1 || len(gate.active) != 1 || gate.completed != 0 {
		t.Fatal("reserved root released without authenticated absent-task fence")
	}
	if _, stopped := a.stopped.Load(testTask); stopped {
		t.Fatal("missing service identity produced an IAM confirmation")
	}
	if err := os.WriteFile(r.Client.Settings.ServiceTokenFile, []byte("synthetic-service-token"), 0600); err != nil {
		t.Fatal(err)
	}
	r.RetryFinishes()
	if len(r.tasks) != 0 || len(gate.active) != 0 || gate.completed != 1 {
		t.Fatal("confirmed absent-task fence did not drain reservation")
	}
	if _, stopped := a.stopped.Load(testTask); !stopped {
		t.Fatal("reserved root lacks IAM tombstone")
	}
}

func TestAcceptedTaskFinishSurvivesCanceledHostCall(t *testing.T) {
	r, a := fixture(t)
	gate := &lifecycleAdmission{}
	r.Admission = gate
	ctx, cancel := context.WithCancel(t.Context())
	b, _ := wire(a.binding)
	raw, _ := json.Marshal(map[string]any{"binding": b, "taskId": testTask, "scopes": []string{"task.execute"}, "audience": "auriga.shared.enterprise"})
	if _, err := r.invoke(ctx, "beginTask", raw); err != nil {
		t.Fatal(err)
	}
	cancel()
	finish, _ := json.Marshal(map[string]any{"binding": b, "taskId": testTask})
	if _, err := r.invoke(t.Context(), "finishTask", finish); err != nil {
		t.Fatal(err)
	}
	if len(r.tasks) != 0 || len(r.tokens) != 0 || gate.completed != 1 {
		t.Fatal("accepted task did not release after host cancellation")
	}
}

func TestCapabilityRefreshAfterFinishCannotRemember(t *testing.T) {
	r, a := fixture(t)
	gate := &lifecycleAdmission{}
	r.Admission = gate
	begin(t, r, a, "auriga.shared.enterprise", []string{"task.execute"})
	a.issueStarted, a.issueResume = make(chan struct{}, 1), make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-a.issueResume:
		default:
			close(a.issueResume)
		}
	})
	b, _ := wire(a.binding)
	raw, _ := json.Marshal(map[string]any{"binding": b, "taskId": testTask, "audience": "auriga.shared.enterprise", "scopes": []string{"task.execute"}})
	done := make(chan error, 1)
	go func() { _, err := r.invoke(t.Context(), "issueCapability", raw); done <- err }()
	awaitAdmission(t, a.issueStarted)
	if err := r.finish(a.binding, testTask); err != nil {
		t.Fatal(err)
	}
	close(a.issueResume)
	if err := awaitAdmissionError(t, done); err == nil {
		t.Fatal("late refresh survived Finish")
	}
	if len(r.tasks) != 0 || len(r.tokens) != 0 {
		t.Fatal("late refresh resurrected local authority")
	}
}

func TestChildRequiresExactOrdinaryParentAndClosesWithIt(t *testing.T) {
	r, a := fixture(t)
	gate := &lifecycleAdmission{}
	r.Admission = gate
	begin(t, r, a, "auriga.shared.enterprise", []string{"task.execute"})
	b, _ := wire(a.binding)
	for _, parent := range []string{"", "forged-synthetic-parent", "synthetic-capability-1"} {
		in := map[string]any{"binding": b, "taskId": testPool, "audience": "auriga.shared.enterprise", "scopes": []string{"outbound"}}
		if parent != "" {
			in["parentCapability"] = parent
		}
		raw, _ := json.Marshal(in)
		_, err := r.invoke(t.Context(), "beginTask", raw)
		if (parent == "synthetic-capability-1") != (err == nil) {
			t.Fatal("parent proof mismatch", parent != "", err)
		}
	}
	if err := r.finish(a.binding, testTask); err != nil {
		t.Fatal(err)
	}
	if len(r.tasks) != 0 || len(r.tokens) != 0 || gate.completed != 1 {
		t.Fatal("parent did not close children and release exactly one slot")
	}
}
