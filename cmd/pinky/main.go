// Command pinky is the Pinky gateway, desktop app and MCP shim in one
// binary.
//
//	pinky            desktop app (gateway + client)
//	pinky daemon     gateway only, no UI
//	pinky mcp        stdio MCP server for agents; forwards to the gateway
//	pinky devices    list connected phones
//	pinky version    print the version
//
// Add --fake to daemon, mcp or devices to use scripted fake phones instead
// of real ones.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "0.0.0-dev"

func main() {
	cmd := "app"
	args := os.Args[1:]
	if len(args) > 0 {
		cmd, args = args[0], args[1:]
	}
	var err error
	switch cmd {
	case "version", "--version", "-v":
		fmt.Println("pinky", version)
	case "help", "--help", "-h":
		usage()
	case "app":
		err = errors.New("the desktop app is not built into this binary yet; run `pinky daemon` for the gateway")
	case "daemon":
		err = runDaemon(args)
	case "mcp":
		err = runMCP(args)
	case "devices":
		err = runDevices(args)
	case "needs":
		err = runNeeds(args)
	case "decide":
		err = runDecide(args)
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			os.Exit(2)
		}
		fmt.Fprintf(os.Stderr, "pinky %s: %v\n", cmd, err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `Usage: pinky [command] [flags]

Commands:
  (none)    start the desktop app
  daemon    run the gateway without UI
  mcp       run the MCP server on stdio (agents launch this); starts the
            gateway when none is running
  devices   list connected phones
  version   print the version

Flags:
  --fake    use scripted fake phones (daemon, mcp, devices)

Environment:
  PINKY_HOME  keep config, data and the socket under this folder
`)
}
