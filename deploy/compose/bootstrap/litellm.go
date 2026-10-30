package main

import (
	"path/filepath"

	"github.com/hansjone/auriga/deploy/bootstrap/materials"
)

func writeLiteLLM(out, adminOrigin string) {
	if err := materials.EnsureLiteLLMSecrets(filepath.Join(out, "litellm")); err != nil {
		fatalf("%v", err)
	}
	writeFile(filepath.Join(out, "litellm-public.env"), []byte("PROXY_BASE_URL="+adminOrigin+"/ai\n"), 0644)
	rpc := map[string]string{"Target": "host.docker.internal:9091", "ServiceTokenFile": "/run/auriga-network-token/gateway/service.jwt", "CertFile": "/etc/auriga/pki/gateway.crt", "KeyFile": "/etc/auriga/pki/gateway.key", "CAFile": "/etc/auriga/pki/ca.crt", "ServerName": "platform"}
	writeJSON(filepath.Join(out, "litellm-runtime.json"), map[string]any{"listen": "0.0.0.0:18445", "tlsCertFile": "/etc/auriga/pki/gateway.crt", "tlsKeyFile": "/etc/auriga/pki/gateway.key", "origin": "http://litellm:4000", "keyFile": "/run/secrets/litellm_team", "iam": rpc, "org": rpc}, 0644)
}
