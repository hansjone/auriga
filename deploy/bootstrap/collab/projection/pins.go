package projection

import (
	"bytes"
	"crypto/ecdh"
	"crypto/x509"

	api "github.com/hansjone/auriga/infra/gen/msgnetapi"
)

func ValidatePins(pins api.InstallationPins) error {
	identities, targets := map[string]bool{}, map[string]bool{}
	for _, pin := range pins.Pins {
		if identities[pin.IdentityId] || targets[pin.TargetId] {
			return Fault("scope_denied")
		}
		identities[pin.IdentityId] = true
		targets[pin.TargetId] = true
		if e := validatePin(pin); e != nil {
			return e
		}
	}
	return nil
}
func validatePin(pin api.InstallationPin) error {
	if pin.IdentityId == "" || pin.TargetId == "" || pin.GatewayId == "" || pin.Subject == "" || len(pin.AdditionalProperties) != 0 || (pin.Component != "network" && pin.Component != "broker" && pin.Component != "edge") {
		return Fault("scope_denied")
	}
	if _, e := public(pin.SigningSpki); e != nil {
		return e
	}
	raw, e := binaryText(pin.SealingSpki)
	if e != nil {
		return e
	}
	v, e := x509.ParsePKIXPublicKey(raw)
	if e != nil {
		return Fault("invalid_chain")
	}
	key, ok := v.(*ecdh.PublicKey)
	if !ok || key.Curve() != ecdh.X25519() {
		return Fault("invalid_chain")
	}
	encoded, e := x509.MarshalPKIXPublicKey(key)
	if e != nil || !bytes.Equal(raw, encoded) {
		return Fault("invalid_chain")
	}
	return nil
}
func RequireProjectorUID(component string) error {
	if component != "edge" && component != "network" && component != "broker" {
		return Fault("scope_denied")
	}
	return checkListenerUID(readerUID(component))
}

func ValidateNodeControlPins(pins api.NodeControlPins) error {
	identities, targets, nodes := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, pin := range pins.Pins {
		if pin.IdentityId == "" || pin.TargetId == "" || pin.NodeId == "" || identities[pin.IdentityId] || targets[pin.TargetId] || nodes[pin.NodeId] {
			return Fault("scope_denied")
		}
		if _, e := number(pin.IdentityRevision); e != nil || pin.IdentityRevision == "0" {
			return Fault("invalid_chain")
		}
		if _, e := public(pin.SigningSpki); e != nil {
			return e
		}
		identities[pin.IdentityId], targets[pin.TargetId], nodes[pin.NodeId] = true, true, true
	}
	return nil
}
