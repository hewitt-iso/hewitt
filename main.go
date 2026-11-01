package main

import (
	"fmt"
	"os"

	"github.com/hewitt-iso/hewitt/cmd"
)

// version is set with -ldflags at build time.
var version = "dev"

func main() {
	if err := cmd.NewRootCommand(version).Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
}
