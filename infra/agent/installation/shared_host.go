// Package installation exposes fixed installation operations without exposing
// the Agent command dispatcher or any runtime command execution.
package installation

import (
	"io"

	"github.com/hansjone/auriga/infra/agent/internal/app"
	"github.com/hansjone/auriga/infra/agent/internal/renderer"
)

// SharedHostInputs contains only the original renderer's public installation
// inputs. The output must be a new absolute path; the renderer owns validation
// and exclusive creation. No private key or service token belongs here.
type SharedHostInputs struct {
	IdentityKeysFile     string
	EnterpriseKeysFile   string
	ModelKeysFile        string
	MessageKeysFile      string
	EnterprisePolicyFile string
	MessageTrustFile     string
	Model                string
	OutputFile           string
}

// ValidateSharedProfile uses the same production pool policy as Agent startup.
// Installation must reject an invalid full profile before committing materials.
func ValidateSharedProfile(profile []byte) error {
	return renderer.ValidateProductionPoolProfile(profile)
}

// RenderSharedHostConfig invokes the existing renderer using a fixed command
// and named flags. Values are passed as separate arguments, never shell text.
func RenderSharedHostConfig(in SharedHostInputs) error {
	args := []string{
		"render-shared-host-config",
		"--identity-keys", in.IdentityKeysFile,
		"--enterprise-keys", in.EnterpriseKeysFile,
		"--model-keys", in.ModelKeysFile,
		"--message-keys", in.MessageKeysFile,
		"--enterprise-config", in.EnterprisePolicyFile,
		"--model", in.Model,
		"--output", in.OutputFile,
	}
	if in.MessageTrustFile != "" {
		args = append(args, "--message-trust", in.MessageTrustFile)
	}
	return app.RunCLI(args, io.Discard, io.Discard)
}
