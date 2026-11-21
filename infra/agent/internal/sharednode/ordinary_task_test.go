package sharednode

import (
	"context"
	"encoding/json"
	"testing"

	iamv1 "github.com/hansjone/auriga/infra/gen/auriga/iam/v1"
)

func TestOrdinaryTaskCredentialStateAndScopeFence(t *testing.T) {
	for _, state := range []iamv1.SharedEnterpriseCredentialState{
		iamv1.SharedEnterpriseCredentialState_SHARED_ENTERPRISE_CREDENTIAL_STATE_ABSENT,
		iamv1.SharedEnterpriseCredentialState_SHARED_ENTERPRISE_CREDENTIAL_STATE_AVAILABLE,
		iamv1.SharedEnterpriseCredentialState_SHARED_ENTERPRISE_CREDENTIAL_STATE_UNSPECIFIED, 99,
	} {
		t.Run(state.String(), func(t *testing.T) {
			r, a := fixture(t)
			a.credentialState.Store(int32(state))
			begin(t, r, a, "auriga.shared.enterprise", []string{"task.execute"})
			claims, _ := wire(r.tokens["synthetic-capability-1"])
			raw, _ := json.Marshal(map[string]any{"capability": claims})
			value, err := r.invoke(t.Context(), "authorizeCapability", raw)
			if state == 0 || state == 99 {
				if err == nil {
					t.Fatal("unknown/zero company state accepted", value)
				}
				return
			}
			if err != nil || value.(map[string]any)["enterpriseCredentialState"] != state.String() {
				t.Fatal("typed company state lost", value, err)
			}
			if !sameStrings(r.tasks[testTask].scopes, []string{"task.execute"}) {
				t.Fatal("authority response upgraded remembered scopes")
			}
			// Company-token rotation must not revoke the independent ordinary task.
			a.version.Add(1)
			if _, err = r.invoke(t.Context(), "authorizeCapability", raw); err != nil {
				t.Fatal("ordinary task coupled to UAC version", err)
			}
			if _, err = r.invoke(t.Context(), "readCredentials", raw); err == nil || err.Error() != "common.forbidden" {
				t.Fatal("generic task reached raw credentials", err)
			}
			outbound, _ := json.Marshal(map[string]any{"capability": claims, "url": "https://business.example.com/path", "method": "POST", "headers": map[string]string{}, "bodyBase64": "", "timeoutMs": 3000})
			if _, err = r.invoke(t.Context(), "enterpriseOutbound", outbound); err == nil || err.Error() != "common.forbidden" {
				t.Fatal("generic task borrowed enterprise outbound", err)
			}
			binding, _ := wire(a.binding)
			upgrade, _ := json.Marshal(map[string]any{"binding": binding, "taskId": testTask, "audience": "auriga.shared.enterprise", "scopes": []string{"outbound"}})
			if _, err = r.invoke(t.Context(), "issueCapability", upgrade); err == nil || err.Error() != "common.forbidden" {
				t.Fatal("remembered generic scope upgraded", err)
			}
			a.invalidSession.Store(true)
			if _, err = r.invoke(t.Context(), "authorizeCapability", raw); err == nil {
				t.Fatal("invalid platform session became absent-material success")
			}
		})
	}
	for _, scopes := range [][]string{{"task.execute", "outbound"}, {"task.execute", "credentials"}, {"shell"}, {"task.execute", "task.execute"}} {
		r, a := fixture(t)
		binding, _ := wire(a.binding)
		raw, _ := json.Marshal(map[string]any{"binding": binding, "taskId": testTask, "audience": "auriga.shared.enterprise", "scopes": scopes})
		if _, err := r.invoke(context.Background(), "beginTask", raw); err == nil {
			t.Fatal("invalid/mixed ordinary scope admitted", scopes)
		}
	}
}

func TestOrdinaryAndEnterpriseTasksKeepSeparateHandles(t *testing.T) {
	r, a := fixture(t)
	begin(t, r, a, "auriga.shared.enterprise", []string{"task.execute"})
	binding, _ := wire(a.binding)
	raw, _ := json.Marshal(map[string]any{"binding": binding, "taskId": testPool, "audience": "auriga.shared.enterprise", "scopes": []string{"outbound"}, "parentCapability": "synthetic-capability-1"})
	if _, err := r.invoke(t.Context(), "beginTask", raw); err != nil {
		t.Fatal("independent enterprise child not admitted", err)
	}
	if _, err := r.capability(t.Context(), "synthetic-capability-"+testPool); err != nil {
		t.Fatal(err)
	}
	raw, _ = json.Marshal(map[string]any{"binding": binding, "taskId": testPool})
	if _, err := r.invoke(t.Context(), "finishTask", raw); err != nil {
		t.Fatal(err)
	}
	if _, err := r.capability(t.Context(), "synthetic-capability-1"); err != nil {
		t.Fatal("child finish closed generic task", err)
	}
	if _, err := r.capability(t.Context(), "synthetic-capability-"+testPool); err == nil {
		t.Fatal("closed child still active")
	}
}
