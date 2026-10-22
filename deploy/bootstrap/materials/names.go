package materials

import (
	"fmt"
	"strings"
)

func validLeaf(name string) bool {
	return name != "" && name != "." && name != ".." && !strings.ContainsAny(name, "/\\\x00:")
}

func checkLeaf(name string) error {
	if !validLeaf(name) {
		return fmt.Errorf("invalid private IAM installation filename")
	}
	return nil
}
