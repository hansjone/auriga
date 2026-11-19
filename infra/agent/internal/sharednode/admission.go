package sharednode

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	iamv1 "github.com/hansjone/auriga/infra/gen/auriga/iam/v1"
	nodev1 "github.com/hansjone/auriga/infra/gen/auriga/node/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

type TaskAdmission interface {
	Admit(context.Context, *nodev1.SharedDshSessionBinding, string) (bool, error)
	Check(*nodev1.SharedDshSessionBinding, string) error
	Complete(*nodev1.SharedDshSessionBinding, string) error
}

func executionCurrent(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if check, ok := ctx.Value(executionFenceKey{}).(func() error); ok {
		return check()
	}
	return nil
}

func (r *Relay) checkAdmission(t execution, id string) error {
	if r.Admission == nil {
		return errors.New("common.unavailable")
	}
	if !t.ordinary {
		r.mu.Lock()
		parent, ok := r.tasks[t.parent]
		r.mu.Unlock()
		if !ok || parent.serial != t.parentSerial || !parent.ordinary || parent.closing || parent.peer != t.peer || !proto.Equal(parent.binding, t.binding) {
			return errors.New("shared.revoked")
		}
		t, id = parent, t.parent
	}
	if !t.reserved {
		return errors.New("shared.revoked")
	}
	if err := r.Admission.Check(t.binding, id); err != nil {
		return errors.New("shared.revoked")
	}
	return nil
}

func (r *Relay) beginTask(ctx context.Context, raw []byte) (any, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var object map[string]json.RawMessage
	if json.Unmarshal(raw, &object) != nil {
		return nil, errors.New("common.validation_failed")
	}
	var parentToken string
	if token, exists := object["parentCapability"]; exists {
		if json.Unmarshal(token, &parentToken) != nil || parentToken == "" {
			return nil, errors.New("common.validation_failed")
		}
		delete(object, "parentCapability")
	}
	request, _ := json.Marshal(object)
	in := new(iamv1.BeginSharedTaskRequest)
	if decode(request, in) != nil || !uuid.MatchString(in.TaskId) {
		return nil, errors.New("common.validation_failed")
	}
	if in.Audience == "" {
		in.Audience = "auriga.shared.enterprise"
	}
	if !validTaskScopes(in.Audience, in.Scopes) {
		return nil, errors.New("common.forbidden")
	}
	if r.Admission == nil {
		return nil, errors.New("common.unavailable")
	}
	if err := executionCurrent(ctx); err != nil {
		return nil, err
	}
	if err := r.session(ctx, in.Binding); err != nil {
		return nil, err
	}
	ordinary := in.Audience == "auriga.shared.enterprise" && sameStrings(in.Scopes, []string{"task.execute"})
	parentID := ""
	var parentSerial uint64
	if ordinary {
		if parentToken != "" {
			return nil, errors.New("common.forbidden")
		}
	} else {
		r.mu.Lock()
		if cap := r.tokens[parentToken]; cap != nil {
			parentSerial = r.tasks[cap.TaskId].serial
		}
		r.mu.Unlock()
		parent, err := r.capability(ctx, parentToken)
		if err != nil || parent.Audience != "auriga.shared.enterprise" || !sameStrings(parent.Scopes, []string{"task.execute"}) || !proto.Equal(parent.Binding, in.Binding) {
			return nil, errors.New("shared.revoked")
		}
		parentID = parent.TaskId
	}
	r.mu.Lock()
	if _, ended := r.ended[in.TaskId]; ended {
		r.mu.Unlock()
		return nil, errors.New("common.conflict")
	}
	// Child registration and parent Finish's closing transition share this
	// boundary. A delayed authorization is not evidence the parent is live.
	if !ordinary {
		parent, exists := r.tasks[parentID]
		if !exists || parent.serial != parentSerial || parent.closing || !parent.ordinary || !parent.reserved || parent.peer != executionPeer(ctx) || !proto.Equal(parent.binding, in.Binding) {
			r.mu.Unlock()
			return nil, errors.New("shared.revoked")
		}
	}
	if old, exists := r.tasks[in.TaskId]; exists {
		if old.closing || old.parent != parentID || old.parentSerial != parentSerial || !proto.Equal(old.binding, in.Binding) || old.audience != in.Audience || !sameStrings(old.scopes, in.Scopes) || old.peer != executionPeer(ctx) {
			r.mu.Unlock()
			return nil, errors.New("common.conflict")
		}
		var token string
		for candidate, cap := range r.tokens {
			if cap.TaskId == in.TaskId && cap.ExpiresAt > time.Now().Unix() {
				token = candidate
				break
			}
		}
		r.mu.Unlock()
		cap, err := r.capability(ctx, token)
		if err != nil {
			return nil, err
		}
		return wire(&iamv1.BeginSharedTaskResponse{Token: token, Capability: cap})
	}
	if len(r.tasks) >= 4096 {
		r.mu.Unlock()
		return nil, errors.New("common.unavailable")
	}
	r.serial++
	t := execution{binding: proto.Clone(in.Binding).(*nodev1.SharedDshSessionBinding), scopes: append([]string{}, in.Scopes...), audience: in.Audience,
		peer: executionPeer(ctx), serial: r.serial, ordinary: ordinary, parent: parentID, parentSerial: parentSerial, started: make(chan struct{})}
	r.tasks[in.TaskId] = t
	r.mu.Unlock()
	out := new(iamv1.BeginSharedTaskResponse)
	err := func() error {
		if ordinary {
			reserved, err := r.Admission.Admit(ctx, in.Binding, in.TaskId)
			r.mu.Lock()
			current := r.tasks[in.TaskId]
			current.reserved = reserved
			r.tasks[in.TaskId] = current
			r.mu.Unlock()
			if err != nil {
				return errors.New("common.unavailable")
			}
		}
		if err := executionCurrent(ctx); err != nil {
			return err
		}
		r.mu.Lock()
		current := r.tasks[in.TaskId]
		if current.closing {
			r.mu.Unlock()
			return errors.New("shared.revoked")
		}
		current.begun = true
		r.tasks[in.TaskId] = current
		r.mu.Unlock()
		if err := r.Client.call(ctx, true, "BeginSharedTask", in, out); err != nil {
			var notSent *notDispatchedError
			if errors.As(err, &notSent) {
				r.mu.Lock()
				current := r.tasks[in.TaskId]
				current.begun = false
				r.tasks[in.TaskId] = current
				r.mu.Unlock()
				return err
			}
			switch status.Code(err) {
			case codes.InvalidArgument, codes.PermissionDenied, codes.Unauthenticated, codes.NotFound, codes.FailedPrecondition, codes.AlreadyExists:
			default:
				r.mu.Lock()
				current := r.tasks[in.TaskId]
				current.uncertainBegin = true
				r.tasks[in.TaskId] = current
				r.mu.Unlock()
			}
			return err
		}
		if err := executionCurrent(ctx); err != nil {
			return err
		}
		if err := r.session(ctx, in.Binding); err != nil {
			return err
		}
		r.mu.Lock()
		current = r.tasks[in.TaskId]
		r.mu.Unlock()
		if current.closing {
			return errors.New("shared.revoked")
		}
		if err := r.checkAdmission(current, in.TaskId); err != nil {
			return err
		}
		return r.remember(in.Binding, in.TaskId, out.Token, out.Capability, t.serial)
	}()
	close(t.started)
	if err != nil {
		_ = r.finishExecution(in.TaskId, t.serial)
		return nil, err
	}
	return wire(out)
}

func (r *Relay) finishExecution(id string, serial uint64) error {
	r.mu.Lock()
	t, ok := r.tasks[id]
	if !ok || t.serial != serial {
		r.mu.Unlock()
		return nil
	}
	if t.finishing != nil {
		done := t.finishing
		r.mu.Unlock()
		<-done
		r.mu.Lock()
		current, exists := r.tasks[id]
		r.mu.Unlock()
		if !exists || current.serial != serial {
			return nil
		}
		return current.finishErr
	}
	t.closing, t.finishing = true, make(chan struct{})
	done := t.finishing
	r.tasks[id] = t
	delete(r.sources, id)
	for token, c := range r.tokens {
		if c.TaskId == id {
			delete(r.tokens, token)
		}
	}
	r.mu.Unlock()
	// Wait for any in-flight Begin to finish before committing its tombstone.
	<-t.started
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	r.mu.Lock()
	t = r.tasks[id]
	children := map[string]uint64{}
	for childID, child := range r.tasks {
		if child.parent == id {
			children[childID] = child.serial
		}
	}
	r.mu.Unlock()
	var err error
	for childID, childSerial := range children {
		if childErr := r.finishExecution(childID, childSerial); childErr != nil {
			err = childErr
		}
	}
	if err == nil && (t.begun || t.reserved) {
		out := new(iamv1.FinishSharedTaskResponse)
		err = r.Client.call(ctx, true, "FinishSharedTask", &iamv1.FinishSharedTaskRequest{Binding: t.binding, TaskId: id}, out)
		if err == nil && (!out.Fenced || out.TaskId != id || !proto.Equal(out.Binding, t.binding) || ctx.Err() != nil) {
			err = errors.New("common.unavailable")
		}
	}
	if err == nil && t.reserved {
		err = r.Admission.Complete(t.binding, id)
	}
	r.mu.Lock()
	if err == nil {
		if t.begun || t.reserved {
			r.rememberFinished(id, t)
		}
		delete(r.tasks, id)
	} else {
		t.finishErr = err
		t.finishing = nil
		r.tasks[id] = t
	}
	r.mu.Unlock()
	// All waiters see the final state; failure stays closing and is retriable.
	close(done)
	return err
}

// RetryFinishes compensates exact closing handles only. It never retries a
// Begin, model dispatch or business request, and cannot free a newer task.
func (r *Relay) RetryFinishes() {
	r.mu.Lock()
	pending := map[string]uint64{}
	for id, task := range r.tasks {
		if task.closing && task.finishing == nil {
			pending[id] = task.serial
		}
	}
	r.mu.Unlock()
	for id, serial := range pending {
		_ = r.finishExecution(id, serial)
	}
}
