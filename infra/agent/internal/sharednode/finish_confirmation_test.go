package sharednode

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
)

func TestFinishRequiresExactIAMFenceBeforeRootReleaseAndUDSEcho(t *testing.T) {
	for mode, name := range []string{"typed", "old-empty", "false", "foreign-task", "foreign-binding", "unknown"} {
		t.Run(name, func(t *testing.T) {
			r, a := fixture(t)
			gate := &lifecycleAdmission{}
			r.Admission = gate
			begin(t, r, a, "auriga.shared.enterprise", []string{"task.execute"})
			b, _ := wire(a.binding)
			child, _ := json.Marshal(map[string]any{"binding": b, "taskId": testPool, "audience": "auriga.shared.enterprise", "scopes": []string{"outbound"}, "parentCapability": "synthetic-capability-1"})
			if _, err := r.invoke(t.Context(), "beginTask", child); err != nil {
				t.Fatal(err)
			}
			a.finishMode.Store(int32(mode))
			raw, _ := json.Marshal(map[string]any{"binding": b, "taskId": testTask})
			value, err := r.invoke(t.Context(), "finishTask", raw)
			if mode != 0 {
				if err == nil || value != nil {
					t.Fatal("unconfirmed IAM Finish produced UDS success", value)
				}
				r.mu.Lock()
				root, child := r.tasks[testTask], r.tasks[testPool]
				tokens := len(r.tokens)
				r.mu.Unlock()
				if !root.closing || !child.closing || tokens != 0 || gate.completed != 0 || len(gate.active) != 1 {
					t.Fatal("unconfirmed child freed root or retained permissions")
				}
				a.finishMode.Store(0)
				r.RetryFinishes()
				value, err = r.invoke(t.Context(), "finishTask", raw)
			}
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(value, map[string]any{"fenced": true, "binding": b, "taskId": testTask}) {
				t.Fatal("inexact UDS finish echo", value)
			}
			if gate.completed != 1 || len(gate.active) != 0 || len(r.tasks) != 0 {
				t.Fatal("confirmed finish leaked or double-released root")
			}
			// Lost UDS replies are replayable only for the original full binding
			// and peer; an old ACK never authorizes a fresh task or frees its slot.
			if _, err = r.invoke(t.Context(), "finishTask", raw); err != nil || gate.completed != 1 {
				t.Fatal("ACK retry mutated slots", err)
			}
			// Use the wire shape to change a field without trusting proto JSON.
			badBinding := map[string]any{}
			for k, v := range b.(map[string]any) {
				badBinding[k] = v
			}
			badBinding["authGeneration"] = 2
			bad, _ := json.Marshal(map[string]any{"binding": badBinding, "taskId": testTask})
			if _, err = r.invoke(t.Context(), "finishTask", bad); err == nil {
				t.Fatal("foreign binding replayed ACK")
			}
			if _, err = r.invoke(context.WithValue(t.Context(), executionPeerKey{}, "foreign-process"), "finishTask", raw); err == nil {
				t.Fatal("foreign peer replayed ACK")
			}
		})
	}
}

func TestUnknownIAMBeginRequiresTombstoneBeforeReleasingRoot(t *testing.T) {
	r, a := fixture(t)
	gate := &lifecycleAdmission{}
	r.Admission = gate
	a.finishMode.Store(1)
	a.beginStarted, a.beginResume = make(chan struct{}, 1), make(chan struct{})
	a.beginEnded = make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-a.beginResume:
		default:
			close(a.beginResume)
		}
	})
	ctx, cancel := context.WithCancel(t.Context())
	b, _ := wire(a.binding)
	raw, _ := json.Marshal(map[string]any{"binding": b, "taskId": testTask, "audience": "auriga.shared.enterprise", "scopes": []string{"task.execute"}})
	done := make(chan error, 1)
	go func() { _, err := r.invoke(ctx, "beginTask", raw); done <- err }()
	awaitAdmission(t, a.beginStarted)
	cancel()
	if err := awaitAdmissionError(t, done); err == nil {
		t.Fatal("unknown Begin published handle")
	}
	if gate.completed != 0 || len(gate.active) != 1 {
		t.Fatal("empty absent Finish released unknown Begin")
	}
	a.finishMode.Store(0)
	r.RetryFinishes()
	if gate.completed != 1 || len(r.tasks) != 0 {
		t.Fatal("typed absent-task tombstone did not drain root")
	}
	close(a.beginResume)
	awaitAdmission(t, a.beginEnded)
	// The synthetic IAM shares its ended map between Begin and Finish, matching
	// the frozen authority rule. The production database proof belongs to root.
	if _, ended := a.stopped.Load(testTask); !ended {
		t.Fatal("no ended authority")
	}
}
