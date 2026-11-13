package renderer

import _ "embed"

//go:embed resource_observer.mjs
var resourceObserver []byte

//go:embed resource_supervisor.mjs
var resourceSupervisor []byte

const ContainerResourceSupervisor = ContainerMetaRoot + "/resource-supervisor.mjs"

const ContainerResourceObserver = ContainerMetaRoot + "/resource-observer.mjs"
const ContainerResourcePolicy = ContainerMetaRoot + "/resource-session-policy.json"

// InstallResourceObserver writes only renderer-owned code, never a spec-supplied module.
func installResourceObserver(opt Options) error {
	if err := writeAtomic(opt.MetaDir+"/resource-supervisor.mjs", resourceSupervisor, 0644, nil, opt); err != nil {
		return err
	}
	return writeAtomic(opt.MetaDir+"/resource-observer.mjs", resourceObserver, 0644, nil, opt)
}
