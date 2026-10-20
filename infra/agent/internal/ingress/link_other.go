//go:build !windows

package ingress

import "os"

func isLink(fi os.FileInfo) bool { return fi.Mode()&(os.ModeSymlink|os.ModeIrregular) != 0 }
