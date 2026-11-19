// Command thingsctl is a token-lean CLI (and, later, MCP server) for Things 3.
package main

import (
	"os"

	"github.com/Frozelo/thingsctl/internal/cli"
)

func main() {
	os.Exit(cli.Main(os.Args[1:], os.Stdout, os.Stderr))
}
