package conn_test

import (
	"testing"
	"time"

	"github.com/hansjone/auriga/infra/agent/internal/conn"
)

func TestSessionDefaults(t *testing.T) {
	s := conn.New(conn.Config{})
	if s == nil {
		t.Fatal("nil")
	}
	if s.Connected() {
		t.Fatal("should start disconnected")
	}
	_ = time.Second
}
