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
	"strings"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "0.0.0-dev"

func main() {
	cmd := "app"
	args := os.Args[1:]
	if len(args) > 0 && (!strings.HasPrefix(args[0], "-") || isCommandFlag(args[0])) {
		cmd, args = args[0], args[1:]
	}
	var err error
	switch cmd {
	case "version", "--version", "-v":
		fmt.Println("pinky", version)
	case "help", "--help", "-h":
		usage()
	case "app":
		err = runApp(args)
	case "daemon":
		err = runDaemon(args)
	case "mcp":
		err = runMCP(args)
	case "devices":
		err = runDevices(args)
	case "companion":
		err = runCompanion(args)
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

// isCommandFlag reports whether a first argument that looks like a flag is
// really a command, such as --version.
func isCommandFlag(arg string) bool {
	switch arg {
	case "--version", "-v", "help", "--help", "-h", "-help":
		return true
	}
	return false
}

func usage() {
	fmt.Fprint(os.Stderr, `Usage: pinky [command] [flags]

Commands:
  (none)    start the desktop app (gateway and window); same as "app"
  daemon    run the gateway without UI
  mcp       run the MCP server on stdio (agents launch this); starts the
            gateway when none is running
  devices   list connected phones
  companion install [phone]
            install or update the Android companion app
  version   print the version

Flags:
  --fake    use scripted fake phones (app, daemon, mcp, devices)

Environment:
  PINKY_HOME  keep config, data and the socket under this folder
`)
}
