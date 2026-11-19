//go:build eval

// Command simserver is `thingsctl mcp --dry-run` whose validated writes are
// applied to $THINGS_DB by the eval simulator, so agents see their changes.
package main

import (
	"os"

	"github.com/Frozelo/thingsctl/eval"
	"github.com/Frozelo/thingsctl/internal/cli"
)

func main() {
	sim := &eval.Sim{Path: os.Getenv("THINGS_DB")}
	cli.Simulate = sim.Apply
	os.Exit(cli.Main(append([]string{"mcp", "--dry-run"}, os.Args[1:]...), os.Stdout, os.Stderr))
}
