// Command pinky is the Pinky gateway, desktop app and MCP shim in one
// binary.
//
//	pinky            desktop app (gateway + client)
//	pinky daemon     gateway only, no UI
//	pinky mcp        stdio MCP server for agents; forwards to the gateway
//	pinky devices    list connected phones
//	pinky token      print the agent token, for agents that connect over HTTP
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

	"github.com/DIYgod/pinky/internal/api"
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
	case "token":
		err = runToken(args)
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
  token     print the agent token, which agents connecting over HTTP send
  version   print the version

Flags:
  --fake    use scripted fake phones (app, daemon, mcp, devices)
  --http addr
            daemon: also serve MCP over HTTP at addr (127.0.0.1:7465)

Environment:
  PINKY_HOME  keep config, data and the socket under this folder
`)
}

// runToken prints the agent token, creating it when missing. Agents that
// connect over HTTP send it as a bearer token; `pinky mcp` reads it itself.
func runToken(args []string) error {
	fs := flag.NewFlagSet("pinky token", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	dirs, err := api.DefaultDirs()
	if err != nil {
		return err
	}
	if err := dirs.Ensure(); err != nil {
		return err
	}
	tok, err := api.EnsureToken(dirs.TokenFile())
	if err != nil {
		return err
	}
	fmt.Println(tok)
	return nil
}
