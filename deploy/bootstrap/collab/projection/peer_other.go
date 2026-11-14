//go:build !linux

package projection

import (
	"errors"
	"net"
)

func checkPeer(net.Conn, uint32) error { return errors.New("projection: SO_PEERCRED requires Linux") }
func checkListenerUID(uint32) error    { return errors.New("projection: SO_PEERCRED requires Linux") }
func setSocketGroup(string) error      { return errors.New("projection: socket permissions require Linux") }
func lockListener(string) (func(), error) {
	return nil, errors.New("projection: listener lock requires Linux")
}
func clearStaleSocket(string, uint32) error {
	return errors.New("projection: socket permissions require Linux")
}
func privateMetadata(string, bool) error {
	return errors.New("projection: private material metadata requires Linux")
}
func lockFile(string) (func(), error) { return nil, Fault("projection_pending") }
