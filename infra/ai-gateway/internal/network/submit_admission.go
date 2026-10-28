package network

import "sync"

// Reserve room for other tenants and other devices within a tenant. These
// limits apply to in-flight submissions, not sustained message rates.
const (
	submitTenantLimit = mailboxWorkers / 4
	submitDeviceLimit = submitTenantLimit / 4
)

type submitDeviceIdentity struct {
	tenant string
	device string
}

// Only IAM-verified identities with a current unique binding reach admission.
// All three budgets change atomically; rejected requests allocate no state.
// Zero counts are deleted so state is bounded by active submissions.
type submitAdmission struct {
	mu      sync.Mutex
	active  int
	tenants map[string]int
	devices map[submitDeviceIdentity]int
}

func (a *submitAdmission) acquire(tenant, device string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	id := submitDeviceIdentity{tenant: tenant, device: device}
	if a.active >= mailboxWorkers || a.tenants[tenant] >= submitTenantLimit || a.devices[id] >= submitDeviceLimit {
		return false
	}
	if a.tenants == nil {
		a.tenants = make(map[string]int)
		a.devices = make(map[submitDeviceIdentity]int)
	}
	a.active++
	a.tenants[tenant]++
	a.devices[id]++
	return true
}

func (a *submitAdmission) release(tenant, device string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	id := submitDeviceIdentity{tenant: tenant, device: device}
	a.active--
	a.tenants[tenant]--
	a.devices[id]--
	if a.tenants[tenant] == 0 {
		delete(a.tenants, tenant)
	}
	if a.devices[id] == 0 {
		delete(a.devices, id)
	}
}
