package runtime_test

import (
	"testing"
	"time"
)

func (f *edgeRecoveryFixture) egressInspect() map[string]any {
	labels := map[string]string{"auriga.role": "egress", "auriga.egress.for-edge": "test-edge", "com.docker.compose.project": "test"}
	config := map[string]any{"Labels": labels, "User": "65532:65532", "Entrypoint": []string{"/auriga-egress"}, "Cmd": []string{"--listen", "0.0.0.0:3128"}}
	host := map[string]any{"NetworkMode": "test_default", "ReadonlyRootfs": true, "Privileged": false, "CapDrop": []string{"ALL"}, "SecurityOpt": []string{"no-new-privileges:true"}}
	state := map[string]any{"Running": true, "StartedAt": f.egressStartedAt, "Pid": 43, "Health": map[string]string{"Status": "healthy"}}
	networks := map[string]any{}
	if f.egressAttached {
		alias, network := "auriga-egress.internal", "existing-network-id"
		if f.failure == "egress-alias" {
			alias = "wrong.example.com"
		}
		if f.failure == "egress-network" {
			network = "foreign-network-id"
		}
		networks["auriga-net-recovery-test"] = map[string]any{"NetworkID": network, "Aliases": []string{alias}}
	}
	result := map[string]any{"Id": f.egressID, "Image": fixtureConfigID, "Config": config, "State": state, "HostConfig": host, "NetworkSettings": map[string]any{"Networks": networks}}
	policyMount := map[string]any{"Type": "volume", "Name": "test_egress-policies", "Destination": "/var/auriga-egress/policies", "RW": false}
	reportMount := map[string]any{"Type": "volume", "Name": "test_egress-reports", "Destination": "/var/auriga-egress/reports", "RW": true}
	result["Mounts"] = []any{policyMount, reportMount}
	switch f.failure {
	case "egress-legacy":
		delete(result, "Mounts")
	case "egress-reversed-volumes":
		result["Mounts"] = []any{reportMount, policyMount}
	case "egress-policy-writable":
		policyMount["RW"] = true
	case "egress-report-readonly":
		reportMount["RW"] = false
	case "egress-foreign-volume":
		policyMount["Name"] = "other_egress-policies"
	case "egress-bind-volume":
		policyMount["Type"] = "bind"
	case "egress-wrong-destination":
		policyMount["Destination"] = "/etc/auriga"
	case "egress-duplicate-volume":
		result["Mounts"] = []any{policyMount, policyMount}
	case "egress-extra-volume":
		result["Mounts"] = []any{policyMount, reportMount, map[string]any{"Type": "volume", "Name": "test_extra", "Destination": "/extra", "RW": true}}
	case "egress-partial-volume":
		result["Mounts"] = []any{policyMount}
	case "egress-image":
		result["Image"] = "sha256:other"
	case "egress-project":
		labels["com.docker.compose.project"] = "other"
	case "egress-owner":
		labels["auriga.egress.for-edge"] = "other-edge"
	case "egress-role":
		labels["auriga.role"] = "other"
	case "egress-user":
		config["User"] = "0:0"
	case "egress-command":
		config["Entrypoint"] = []string{"/auriga-edge"}
	case "egress-arguments":
		config["Cmd"] = []string{"--listen", "0.0.0.0:80"}
	case "egress-privileged":
		host["Privileged"] = true
	case "egress-writable":
		host["ReadonlyRootfs"] = false
	case "egress-caps":
		host["CapDrop"] = []string{}
	case "egress-extra-caps":
		host["CapAdd"] = []string{"NET_ADMIN"}
	case "egress-security":
		host["SecurityOpt"] = []string{}
	case "egress-host":
		host["NetworkMode"] = "host"
	case "egress-container-network":
		host["NetworkMode"] = "container:other"
	case "egress-stopped":
		state["Running"] = false
	case "egress-unhealthy":
		state["Health"] = map[string]string{"Status": "unhealthy"}
	case "egress-pid":
		state["Pid"] = 0
	case "egress-started-at":
		state["StartedAt"] = ""
	case "egress-mount":
		result["Mounts"] = []any{map[string]string{"Source": "/unapproved", "Destination": "/unapproved"}}
	case "egress-published":
		host["PortBindings"] = map[string]any{"3128/tcp": []any{map[string]string{"HostPort": "3128"}}}
	}
	return result
}

func egressRecovery(t *testing.T, failure string) *edgeRecoveryFixture {
	t.Helper()
	f := newEdgeRecoveryFixture(t, failure)
	f.driver.EgressContainer = "test-egress"
	f.egressID, f.egressStartedAt = "egress-id", "2026-01-01T01:02:03Z"
	return f
}

func TestDockerEgressJoinsIsolatedNetworkAndRecoversWithoutRestart(t *testing.T) {
	f := egressRecovery(t, "")
	if err := completedEdgeRecoveryStart(t, f, 15*time.Second); err != nil {
		t.Fatal(err)
	}
	if f.connects.Load() != 2 || f.starts.Load() != 1 {
		t.Fatal("edge and egress must both be attached before first publication")
	}
	f.mu.Lock()
	f.egressAttached = false
	f.mu.Unlock()
	st, err := f.driver.CheckHealth(t.Context(), "recovery-test")
	if err != nil || st.Ready || !st.Running || f.connects.Load() != 2 {
		t.Fatal("missing egress was repaired by the read-only check or remained READY", st, err)
	}
	if _, err = f.driver.Endpoint("recovery-test"); err == nil {
		t.Fatal("lost egress retained endpoint")
	}
	if err = completedEdgeRecoveryStart(t, f, 15*time.Second); err != nil {
		t.Fatal(err)
	}
	if f.connects.Load() != 3 || f.starts.Load() != 1 {
		t.Fatal("egress repair restarted DSH or reattached existing edge")
	}
}

func TestDockerEgressLegacyWithoutPolicyMountsStillStarts(t *testing.T) {
	f := egressRecovery(t, "egress-legacy")
	if err := completedEdgeRecoveryStart(t, f, 15*time.Second); err != nil {
		t.Fatal(err)
	}
}

func TestDockerEgressPolicyMountOrderDoesNotAffectStartup(t *testing.T) {
	f := egressRecovery(t, "egress-reversed-volumes")
	if err := completedEdgeRecoveryStart(t, f, 15*time.Second); err != nil {
		t.Fatal(err)
	}
}

func TestDockerEgressRejectsUntrustedIdentityBeforeAnyMutation(t *testing.T) {
	for _, failure := range []string{"missing", "image", "project", "owner", "role", "user", "command", "arguments", "privileged", "writable", "caps", "extra-caps", "security", "host", "container-network", "stopped", "unhealthy", "pid", "started-at", "mount", "published", "alias", "network", "policy-writable", "report-readonly", "foreign-volume", "bind-volume", "wrong-destination", "duplicate-volume", "extra-volume", "partial-volume"} {
		t.Run(failure, func(t *testing.T) {
			f := egressRecovery(t, "egress-"+failure)
			if failure == "alias" || failure == "network" {
				f.egressAttached = true
			}
			if err := completedEdgeRecoveryStart(t, f, 15*time.Second); err == nil {
				t.Fatal("untrusted egress accepted")
			}
			if f.connects.Load() != 0 || f.starts.Load() != 0 {
				t.Fatal("untrusted egress caused Engine writes")
			}
		})
	}
}

func TestDockerEgressRechecksAliasAndLifetimeAfterConnect(t *testing.T) {
	for _, failure := range []string{"noop", "replaced", "lifetime"} {
		t.Run(failure, func(t *testing.T) {
			f := egressRecovery(t, "egress-"+failure)
			if err := completedEdgeRecoveryStart(t, f, 15*time.Second); err == nil {
				t.Fatal("egress CONNECT success was mistaken for stable topology")
			}
			if f.starts.Load() != 0 {
				t.Fatal("DSH started despite unverifiable egress")
			}
		})
	}
}

func TestDockerEgressProcessChangeDuringHealthCannotQualifyReady(t *testing.T) {
	for _, operation := range []string{"check", "start"} {
		t.Run(operation, func(t *testing.T) {
			f := egressRecovery(t, "")
			if err := completedEdgeRecoveryStart(t, f, 15*time.Second); err != nil {
				t.Fatal(err)
			}
			f.mu.Lock()
			f.healthHook = func() {
				f.mu.Lock()
				f.egressStartedAt = "2026-01-01T01:03:04Z"
				f.mu.Unlock()
			}
			f.mu.Unlock()
			if operation == "check" {
				if st, err := f.driver.CheckHealth(t.Context(), "recovery-test"); err == nil || st.Ready {
					t.Fatal("health accepted a changed egress lifetime", st, err)
				}
			} else if err := completedEdgeRecoveryStart(t, f, 15*time.Second); err == nil {
				t.Fatal("start accepted a changed egress lifetime")
			}
			if _, err := f.driver.Endpoint("recovery-test"); err == nil {
				t.Fatal("changed egress lifetime retained endpoint")
			}
		})
	}
}
