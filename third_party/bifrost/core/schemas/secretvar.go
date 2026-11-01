package schemas

import (
	"database/sql/driver"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/bytedance/sonic"
)

type SecretType string
type SecretVar struct {
	Val        string `json:"value"`
	ref        string
	SecretType SecretType `json:"type,omitempty"`
}

const (
	SecretTypePlainText SecretType = "plain_text"
	SecretTypeEnv       SecretType = "env"
	SecretTypeVault     SecretType = "vault"
)

func inferSecretType(ref string) SecretType {
	if strings.HasPrefix(ref, "vault.") {
		return SecretTypeVault
	}
	if strings.HasPrefix(ref, "env.") {
		return SecretTypeEnv
	}
	return SecretTypePlainText
}

func parseSecretRef(value string) *SecretVar {
	val := value
	if unquoted, err := strconv.Unquote(value); err == nil {
		val = unquoted
	}
	if sonic.Valid([]byte(value)) {
		valueNode, _ := sonic.Get([]byte(val), "value")
		refNode, _ := sonic.Get([]byte(val), "ref")
		typeNode, _ := sonic.Get([]byte(val), "type")
		envVarNode, _ := sonic.Get([]byte(val), "env_var")
		fromEnvNode, _ := sonic.Get([]byte(val), "from_env")
		isSecretVarJSON := valueNode.Exists() ||
			(refNode.Exists() && typeNode.Exists()) ||
			(envVarNode.Exists() && fromEnvNode.Exists())
		if isSecretVarJSON {
			type secretVarCompat struct {
				Val        string     `json:"value"`
				Ref        string     `json:"ref"`
				SecretType SecretType `json:"type"`

				EnvVar  string `json:"env_var"`
				FromEnv bool   `json:"from_env"`
			}
			var raw secretVarCompat
			if err := sonic.Unmarshal([]byte(value), &raw); err == nil {
				e := &SecretVar{Val: raw.Val}
				if raw.SecretType != "" {

					e.ref = raw.Ref
					e.SecretType = raw.SecretType
				} else if raw.Ref != "" {

					e.ref = raw.Ref
					e.SecretType = inferSecretType(raw.Ref)
				} else if raw.FromEnv && raw.EnvVar != "" {

					ref := raw.EnvVar
					if !strings.HasPrefix(ref, "env.") {
						ref = "env." + ref
					}
					e.ref = ref
					e.SecretType = SecretTypeEnv
				} else if strings.HasPrefix(raw.Val, "env.") && raw.Val == raw.EnvVar {

					e.ref = raw.EnvVar
					e.SecretType = SecretTypeEnv
				} else {

					e.SecretType = SecretTypePlainText
				}
				return e
			}
		}
	}
	if strings.HasPrefix(val, "vault.") {
		return &SecretVar{ref: val, SecretType: SecretTypeVault}
	}
	if strings.HasPrefix(val, "env.") {
		return &SecretVar{ref: val, SecretType: SecretTypeEnv}
	}
	return &SecretVar{Val: val, SecretType: SecretTypePlainText}
}

func NewSecretVar(value string) *SecretVar {
	e := parseSecretRef(value)
	switch e.SecretType {
	case SecretTypeVault:
		e.Val = ""
		if vaultValue, ok := LookupVault(e.ref); ok {
			e.Val = vaultValue
		}
	case SecretTypeEnv:
		if envValue, ok := os.LookupEnv(e.EnvKey()); ok {
			e.Val = envValue
		} else {
			e.Val = ""
		}
	}
	return e
}

func (e *SecretVar) GetRawRef() string {
	if e == nil {
		return ""
	}
	return e.ref
}

func (e *SecretVar) Type() SecretType {
	if e == nil {
		return SecretTypePlainText
	}
	if e.SecretType == "" {
		return SecretTypePlainText
	}
	return e.SecretType
}

func (e *SecretVar) IsFromSecret() bool {
	if e == nil {
		return false
	}
	return e.SecretType == SecretTypeEnv || e.SecretType == SecretTypeVault
}

func (e *SecretVar) EnvKey() string {
	if e == nil || e.SecretType != SecretTypeEnv {
		return ""
	}
	return strings.TrimPrefix(e.ref, "env.")
}

func (e *SecretVar) Clone() *SecretVar {
	if e == nil {
		return nil
	}
	clone := *e
	return &clone
}

func (e SecretVar) MarshalJSON() ([]byte, error) {
	return Marshal(struct {
		Val        string     `json:"value"`
		Ref        string     `json:"ref,omitempty"`
		SecretType SecretType `json:"type,omitempty"`
	}{Val: e.Val, Ref: e.ref, SecretType: e.SecretType})
}

func (e *SecretVar) UnmarshalJSON(data []byte) error {
	val := string(data)
	if len(data) > 0 && data[0] == '"' {
		var decoded string
		if err := sonic.Unmarshal(data, &decoded); err == nil {
			val = decoded
		}
	}
	if sonic.Valid(data) {
		valueNode, _ := sonic.Get(data, "value")
		refNode, _ := sonic.Get(data, "ref")
		typeNode, _ := sonic.Get(data, "type")
		envVarNode, _ := sonic.Get(data, "env_var")
		fromEnvNode, _ := sonic.Get(data, "from_env")
		isSecretVarJSON := valueNode.Exists() ||
			(refNode.Exists() && typeNode.Exists()) ||
			(envVarNode.Exists() && fromEnvNode.Exists())
		if isSecretVarJSON {
			type secretVarCompat struct {
				Val        string     `json:"value"`
				Ref        string     `json:"ref"`
				SecretType SecretType `json:"type"`

				EnvVar  string `json:"env_var"`
				FromEnv bool   `json:"from_env"`
			}
			var raw secretVarCompat
			if err := sonic.Unmarshal(data, &raw); err == nil {
				e.Val = raw.Val
				if raw.SecretType != "" {

					e.ref = raw.Ref
					e.SecretType = raw.SecretType
				} else if raw.Ref != "" {

					e.ref = raw.Ref
					e.SecretType = inferSecretType(raw.Ref)
				} else if raw.FromEnv && raw.EnvVar != "" {

					ref := raw.EnvVar
					if !strings.HasPrefix(ref, "env.") {
						ref = "env." + ref
					}
					e.ref = ref
					e.SecretType = SecretTypeEnv
					if envValue, ok := os.LookupEnv(strings.TrimPrefix(ref, "env.")); ok {
						e.Val = envValue
					} else {
						e.Val = ""
					}
					return nil
				} else if strings.HasPrefix(raw.Val, "env.") && raw.Val == raw.EnvVar {

					e.ref = raw.EnvVar
					e.SecretType = SecretTypeEnv
					e.Val = ""
					if envValue, ok := os.LookupEnv(strings.TrimPrefix(raw.EnvVar, "env.")); ok {
						e.Val = envValue
					}
					return nil
				}

				if e.SecretType == SecretTypeVault {
					e.Val = ""
					if vaultValue, ok := LookupVault(e.ref); ok {
						e.Val = vaultValue
					}
				}
				if e.SecretType == SecretTypeEnv {
					if envValue, ok := os.LookupEnv(e.EnvKey()); ok {
						e.Val = envValue
					} else {
						e.Val = ""
					}
				}
				if e.SecretType == "" {
					e.SecretType = SecretTypePlainText
				}
				return nil
			}
		}
	}

	if strings.HasPrefix(val, "vault.") {
		e.ref = val
		e.SecretType = SecretTypeVault
		e.Val = ""
		if vaultValue, ok := LookupVault(val); ok {
			e.Val = vaultValue
		}
		return nil
	}
	if envKey, ok := strings.CutPrefix(val, "env."); ok {
		e.ref = val
		e.SecretType = SecretTypeEnv
		if envValue, ok := os.LookupEnv(envKey); ok {
			e.Val = envValue
		} else {
			e.Val = ""
		}
		return nil
	}
	e.Val = val
	e.ref = ""
	e.SecretType = SecretTypePlainText
	return nil
}

func (e *SecretVar) String() string {
	if e == nil {
		return ""
	}
	return e.Val
}

func (e *SecretVar) Scan(value any) error {
	if value == nil {
		e.Val = ""
		e.ref = ""
		e.SecretType = SecretTypePlainText
		return nil
	}
	switch v := value.(type) {
	case []byte:
		return e.Scan(string(v))
	case string:

		if strings.HasPrefix(v, "vault.") {
			e.Val = ""
			e.ref = v
			e.SecretType = SecretTypeVault
			if vaultValue, ok := LookupVault(v); ok {
				e.Val = vaultValue
			}
			return nil
		}
		if envKey, ok := strings.CutPrefix(v, "env."); ok {
			e.ref = v
			e.SecretType = SecretTypeEnv
			if envValue, ok := os.LookupEnv(envKey); ok {
				e.Val = envValue
			} else {
				e.Val = ""
			}
			return nil
		}
		e.Val = strings.Trim(v, "\"")
		e.ref = ""
		e.SecretType = SecretTypePlainText
		return nil
	}
	return fmt.Errorf("failed to scan value: %v", value)
}

func (e SecretVar) Value() (driver.Value, error) {
	if e.IsFromSecret() {
		return e.ref, nil
	}
	return e.Val, nil
}

func (e *SecretVar) GetValue() string {
	if e == nil {
		return ""
	}
	return e.Val
}
