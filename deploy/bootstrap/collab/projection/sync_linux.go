package projection

import "os"

func syncDir(path string) error {
	f, e := os.Open(path)
	if e != nil {
		return e
	}
	defer func() { _ = f.Close() }()
	return f.Sync()
}
