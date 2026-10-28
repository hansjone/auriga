package main

import (
	"encoding/json"
	"fmt"
	"github.com/hansjone/auriga/deploy/bootstrap/network"
	"os"
)

func main() {
	if len(os.Args) != 3 {
		os.Exit(2)
	}
	raw, err := os.ReadFile(os.Args[2])
	var options network.Options
	if err == nil {
		err = json.Unmarshal(raw, &options)
	}
	if err == nil {
		err = network.Generate(os.Args[1], options)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "cannot generate private network deployment material")
		os.Exit(1)
	}
}
