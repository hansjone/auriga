package projection

import (
	"encoding/json"
	"github.com/santhosh-tekuri/jsonschema/v6"
	"os"
)

type Schemas struct{ definitions map[string]*jsonschema.Schema }

// LoadSchemas consumes the frozen schema shipped by D; no parallel handwritten
// wire definitions are maintained by this implementation.
func LoadSchemas(path string) (*Schemas, error) {
	b, e := os.ReadFile(path)
	if e != nil {
		return nil, e
	}
	var doc any
	if e = json.Unmarshal(b, &doc); e != nil {
		return nil, e
	}
	c := jsonschema.NewCompiler()
	c.AssertFormat()
	const location = "https://auriga.example.com/contracts/network/auto-renewal-v2.schema.json"
	if e = c.AddResource(location, doc); e != nil {
		return nil, e
	}
	s := &Schemas{definitions: map[string]*jsonschema.Schema{}}
	for _, name := range []string{"Publication", "SignedRootControl", "SignedRuntimeAuthority", "SignedTransportTrustBundle", "SignedTransportConfirmation", "TransportConfirmationQuery", "TransportPredecessor", "TransportTransitionProof", "IsolationRecord", "NodeControlPins", "ProtocolMaterial", "ProjectionMaterial", "ProjectionRequest", "SealedMaterial", "LocalProjectionManifest", "LocalProjectionSelector", "InstallationPins", "RenewalConfig", "RenewalStatusOutput", "ControlPackage"} {
		schema, e := c.Compile(location + "#/$defs/" + name)
		if e != nil {
			return nil, e
		}
		s.definitions[name] = schema
	}
	return s, nil
}
func (s *Schemas) Decode(name string, b []byte, out any) error {
	if _, e := Canonical(b); e != nil {
		return e
	}
	var v any
	if e := json.Unmarshal(b, &v); e != nil {
		return e
	}
	schema := s.definitions[name]
	if schema == nil {
		return Fault("invalid_chain")
	}
	if e := schema.Validate(v); e != nil {
		return Fault("invalid_chain")
	}
	return Decode(b, out, false)
}
