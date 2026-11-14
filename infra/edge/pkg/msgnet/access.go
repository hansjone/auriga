package msgnet

import api "github.com/hansjone/auriga/infra/gen/msgnetapi"

// NodeBinding describes an authenticated session and trusted installation facts.
// The receipt can supply identity claims but can never supply enrollment or scope.
type NodeBinding struct {
	AuthenticatedNodeID, TargetID, IdentityID, IdentityRevision string
	Compromised, RequestsPrivateMaterial, IndependentlyEnrolled bool
}

func VerifyNodeBinding(pin api.NodeControlPin, binding NodeBinding) error {
	if binding.AuthenticatedNodeID != pin.NodeId || binding.TargetID != pin.TargetId || binding.IdentityID != pin.IdentityId {
		return Fault("scope_denied")
	}
	if binding.IdentityRevision != pin.IdentityRevision {
		return Fault("version_rollback")
	}
	if binding.Compromised {
		return Fault("identity_quarantined")
	}
	if binding.RequestsPrivateMaterial {
		return Fault("scope_denied")
	}
	if !binding.IndependentlyEnrolled {
		return Fault("identity_quarantined")
	}
	return nil
}

type ProjectionAccess struct {
	Component                                  string
	ProjectorUID, ReaderUID, PeerUID, OwnerUID int
	DirectoryMode, FileMode                    int
	ComponentPrivateMount, CapDropAll          bool
}

func VerifyProjectionAccess(f ProjectionAccess) error {
	if f.Component != "edge" && f.Component != "network" && f.Component != "broker" {
		return Fault("scope_denied")
	}
	uid := 10001
	if f.Component == "edge" {
		uid = 65532
	}
	if f.ProjectorUID != uid || f.ReaderUID != uid || f.PeerUID != uid || f.OwnerUID != uid || f.DirectoryMode != 0700 || f.FileMode != 0600 || !f.ComponentPrivateMount || !f.CapDropAll {
		return Fault("scope_denied")
	}
	return nil
}
