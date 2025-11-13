// Package main is the entry point for github-copilot-svcs.
package main

import (
	"os"

	"github.com/xdlhzdh/github-copilot-svcs/internal"
)

// version will be set by the build process
var version = "dev"

func main() {
	// Initialize logger early
	internal.Init()

	const minArgsRequired = 2
	if len(os.Args) < minArgsRequired {
		internal.PrintUsage()
		return
	}

	if err := internal.RunCommand(os.Args[1], os.Args[2:], version); err != nil {
		internal.Error("Command failed", err)
		os.Exit(1)
	}
}
