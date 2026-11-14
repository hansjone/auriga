//go:build windows

package projection

// Windows directory handles cannot be fsynced through os.File. The fixed Unix
// socket reader is unsupported on Windows; shared pure validation still runs.
func syncDirectory(string) error { return nil }
