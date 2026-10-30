package main

import "fmt"

func panelOwnerEnvironment(_ string) ([]byte, error) {
	return nil, fmt.Errorf("container panel owner requires Linux file metadata")
}
