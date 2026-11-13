//go:build !linux && !windows

package runtime

import "fmt"

func unifiedLocalFS(string) error {
	return fmt.Errorf("shared resources require a supported Linux or Windows node")
}
