package conn

import (
	"context"
	"errors"
	nodev1 "github.com/hansjone/auriga/infra/gen/auriga/node/v1"
	"testing"
)

type msgnetInstaller struct {
	fail            bool
	control, bundle []byte
}

func (i *msgnetInstaller) InstallControl(_ context.Context, b []byte) ([]byte, error) {
	i.control = append([]byte(nil), b...)
	if i.fail {
		return nil, errors.New("durability failed")
	}
	return []byte("signed durable receipt"), nil
}
func (i *msgnetInstaller) InstallTransport(_ context.Context, b []byte) error {
	i.bundle = append([]byte(nil), b...)
	return nil
}
func TestMsgnetControlIndependentOfReconciler(t *testing.T) {
	installer := &msgnetInstaller{}
	s := New(Config{Msgnet: installer})
	body := []byte("canonical signed public control")
	msg := &nodev1.SessionResponse{Body: &nodev1.SessionResponse_MsgnetControlPackage{MsgnetControlPackage: &nodev1.MsgnetControlPackage{CanonicalJson: body}}}
	if err := s.handleControl(context.Background(), msg); err != nil {
		t.Fatal(err)
	}
	body[0] = 'X'
	if string(installer.control) != "canonical signed public control" {
		t.Fatal("mutable transport buffer retained")
	}
	select {
	case r := <-s.msgnetReceiptCh:
		if string(r.CanonicalJson) != "signed durable receipt" {
			t.Fatal("receipt changed")
		}
	default:
		t.Fatal("missing receipt")
	}
	installer.fail = true
	if err := s.handleControl(context.Background(), msg); err == nil {
		t.Fatal("failed persistence acknowledged")
	}
	if len(s.msgnetReceiptCh) != 0 {
		t.Fatal("receipt emitted on failure")
	}
	s.cfg.Msgnet = nil
	if err := s.handleControl(context.Background(), msg); err == nil {
		t.Fatal("missing installer silently accepted")
	}
	s.cfg.Msgnet = installer
	bundle := &nodev1.SessionResponse{Body: &nodev1.SessionResponse_MsgnetTransportBundle{MsgnetTransportBundle: &nodev1.MsgnetTransportBundle{CanonicalJson: []byte("public bundle")}}}
	if err := s.handleControl(context.Background(), bundle); err != nil {
		t.Fatal(err)
	}
	if string(installer.bundle) != "public bundle" {
		t.Fatal("bundle lost")
	}
}
