//go:build !linux

package projection

type BrokerSupervisor struct {
	Binary, Template, Control string
	ExpectedSHA256            string
	Trust                     *Trust
}

func (b *BrokerSupervisor) Prepare(*Generation) (func() error, error) {
	return nil, Fault("projection_pending")
}
func (b *BrokerSupervisor) Close()       {}
func (b *BrokerSupervisor) Active() bool { return false }
