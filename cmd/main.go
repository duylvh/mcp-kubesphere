package main

import (
	"fmt"
	"os"

	"github.com/duylvh/mcp-kubesphere/cmd/app"
)

func main() {
	if err := app.NewRootCommand().Execute(); err != nil {
		// stdout is the MCP channel; errors go to stderr.
		fmt.Fprintf(os.Stderr, "%+v\n", err)
		os.Exit(1)
	}
}
