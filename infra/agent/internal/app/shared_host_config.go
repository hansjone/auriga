package app

import (
	"crypto/ed25519"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Installation command: converts existing public JWKS to the Host's SPKI maps.
// No private signing key, service JWT or enterprise credential is read.
func renderSharedHostConfig(args []string, out, logs io.Writer) error {
	fs := flag.NewFlagSet("render-shared-host-config", flag.ContinueOnError)
	fs.SetOutput(logs)
	identity := fs.String("identity-keys", "", "Edge public JWKS file")
	enterprise := fs.String("enterprise-keys", "", "IAM enterprise public JWKS file")
	modelKeys := fs.String("model-keys", "", "IAM model public JWKS file")
	messageKeys := fs.String("message-keys", "", "IAM message public JWKS file")
	policy := fs.String("enterprise-config", "", "public enterprise policy JSON file")
	messageTrust := fs.String("message-trust", "", "optional approved public message realm/roots JSON file")
	model := fs.String("model", "", "authorized public model ID")
	output := fs.String("output", "", "new absolute Host template file")
	if e := fs.Parse(args); e != nil {
		return e
	}
	if fs.NArg() != 0 || *model == "" || len(*model) > 128 || strings.TrimSpace(*model) != *model || !filepath.IsAbs(*output) {
		return fmt.Errorf("public key files, model and absolute new output required")
	}
	seen := map[string]string{}
	keys := func(path, purpose string) (map[string]string, error) {
		raw, e := readPublicInstallation(path)
		if e != nil {
			return nil, e
		}
		var file struct {
			Keys []struct{ Kid, Kty, Crv, X, D string }
		}
		if e = json.Unmarshal(raw, &file); e != nil || len(file.Keys) == 0 {
			return nil, fmt.Errorf("public Ed25519 JWKS required for %s", purpose)
		}
		result := map[string]string{}
		for _, k := range file.Keys {
			b, e := base64.RawURLEncoding.DecodeString(k.X)
			if e != nil || len(b) != ed25519.PublicKeySize || k.Kty != "OKP" || k.Crv != "Ed25519" || k.Kid == "" || len(k.Kid) > 128 || k.D != "" || result[k.Kid] != "" {
				return nil, fmt.Errorf("invalid public JWKS for %s", purpose)
			}
			if prior := seen[k.X]; prior != "" && prior != purpose {
				return nil, fmt.Errorf("public key reused across purposes")
			}
			seen[k.X] = purpose
			der, e := x509.MarshalPKIXPublicKey(ed25519.PublicKey(b))
			if e != nil {
				return nil, e
			}
			result[k.Kid] = string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))
		}
		return result, nil
	}
	ids, e := keys(*identity, "identity")
	if e != nil {
		return e
	}
	purposes := map[string]map[string]string{}
	for purpose, path := range map[string]string{"auriga.shared.enterprise": *enterprise, "auriga.shared.model": *modelKeys, "auriga.shared.message": *messageKeys} {
		k, e := keys(path, purpose)
		if e != nil {
			return e
		}
		purposes[purpose] = k
	}
	raw, e := readPublicInstallation(*policy)
	if e != nil {
		return e
	}
	var enterprisePolicy struct {
		AllowRawAgentToken   bool     `json:"allowRawAgentToken"`
		OutboundAllowedHosts []string `json:"outboundAllowedHosts"`
		EmpNoHeader          string   `json:"empNoHeader"`
		AuthValueHeader      string   `json:"authValueHeader"`
	}
	if e = json.Unmarshal(raw, &enterprisePolicy); e != nil || len(enterprisePolicy.OutboundAllowedHosts) == 0 || enterprisePolicy.EmpNoHeader == "" || enterprisePolicy.AuthValueHeader == "" {
		return fmt.Errorf("complete public enterprise policy required")
	}
	configuration := map[string]any{"identityKeys": ids, "capabilityKeys": purposes, "model": *model, "enterprise": enterprisePolicy}
	if *messageTrust != "" {
		raw, e := readPublicInstallation(*messageTrust)
		if e != nil {
			return e
		}
		var trust struct {
			Realm string `json:"realm"`
			Roots []struct {
				Kid string `json:"kid"`
				Key string `json:"public_key_spki"`
			} `json:"roots"`
		}
		if e = json.Unmarshal(raw, &trust); e != nil || trust.Realm == "" || len(trust.Roots) == 0 {
			return fmt.Errorf("approved message realm and roots required")
		}
		seen := map[string]bool{}
		for _, root := range trust.Roots {
			der, e := base64.RawURLEncoding.Strict().DecodeString(root.Key)
			if e != nil || root.Kid == "" || seen[root.Kid] {
				return fmt.Errorf("invalid message root")
			}
			key, e := x509.ParsePKIXPublicKey(der)
			if _, ok := key.(ed25519.PublicKey); e != nil || !ok {
				return fmt.Errorf("public Ed25519 message root required")
			}
			seen[root.Kid] = true
		}
		configuration["messageTrust"] = trust
	}
	body, e := json.MarshalIndent(configuration, "", "  ")
	if e != nil {
		return e
	}
	if len(body) > 256*1024 {
		return fmt.Errorf("host configuration too large")
	}
	file, e := os.OpenFile(*output, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0444)
	if e != nil {
		return e
	}
	// The installer consumes this public output with an exact 0444 contract.
	// O_EXCL's creation mode is masked by umask; pin it on the same owned fd.
	if e = file.Chmod(0444); e != nil {
		_ = file.Close()
		return e
	}
	_, writeErr := file.Write(append(body, '\n'))
	syncErr := file.Sync()
	closeErr := file.Close()
	if writeErr != nil {
		return writeErr
	}
	if syncErr != nil {
		return syncErr
	}
	if closeErr != nil {
		return closeErr
	}
	_, e = fmt.Fprintln(out, "Rendered shared Host public configuration")
	return e
}
func readPublicInstallation(path string) ([]byte, error) {
	info, e := os.Lstat(path)
	if e != nil {
		return nil, e
	}
	if !filepath.IsAbs(path) || !info.Mode().IsRegular() || info.Size() > 256*1024 {
		return nil, fmt.Errorf("fixed public installation file required")
	}
	return os.ReadFile(path)
}
