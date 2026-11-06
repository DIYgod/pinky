// Command pinky is the Pinky gateway, desktop app and MCP shim in one
// binary.
//
//	pinky            desktop app (gateway + client)
//	pinky daemon     gateway only, no UI
//	pinky mcp        stdio MCP server for agents; forwards to the gateway
//	pinky devices    list connected phones
//	pinky version    print the version
package main

import (
	"fmt"
	"os"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "0.0.0-dev"

func main() {
	cmd := "app"
	if len(os.Args) > 1 {
		cmd = os.Args[1]
	}
	switch cmd {
	case "version", "--version", "-v":
		fmt.Println("pinky", version)
	case "help", "--help", "-h":
		usage()
	case "app", "daemon", "mcp", "devices":
		fmt.Fprintf(os.Stderr, "pinky %s: not implemented yet\n", cmd)
		os.Exit(1)
	default:
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `Usage: pinky [command]

Commands:
  (none)    start the desktop app
  daemon    run the gateway without UI
  mcp       run the MCP server on stdio (agents launch this)
  devices   list connected phones
  version   print the version
`)
}
