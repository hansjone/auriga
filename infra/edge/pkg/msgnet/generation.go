// Package msgnet validates installed message trust for edge and node control.
// The cryptographic foundations are shared with the repository's projector
// implementation at 2d36bd1b. I keeps the consumer integration and K3 policy here
// so the live consumers and the shared-vector tests invoke the same decisions.
package msgnet

import api "github.com/hansjone/auriga/infra/gen/msgnetapi"

// Generation is process-local state, not an additional wire model.
type Generation struct {
	Selector api.LocalProjectionSelector
	Manifest api.LocalProjectionManifest
	Files    map[string][]byte
}

func (g *Generation) Clear() {
	for _, data := range g.Files {
		clear(data)
	}
}
