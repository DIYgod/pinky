# Tailscale command environment

On macOS, the Tailscale app binary also provides the CLI. With Tailscale
1.104.5, running `status --json` in a minimal desktop environment without
terminal variables printed `The Tailscale GUI failed to start: The operation
couldn’t be completed. (Tailscale.CLIError error 3.)` and exited successfully.
Pinky then reported `tailscale status: invalid character 'T' looking for
beginning of value`. Adding `TERM=dumb` to the same environment returned valid
JSON with `BackendState: Running`.

`runCommand` now supplies `TERM=dumb` on macOS when TERM is unset or empty.
This applies to both status and serve status, preserves an existing TERM and
the inherited environment, and does not change Pinky's own environment or
commands on other operating systems. `TestRunCommandTerminal` checks the
actual child environment without a shell that could supply its own TERM.
Run it with `go test ./internal/tailnet`.

The reproduction was checked against the local Tailscale app on macOS; no
phone was operated. This addresses reading the local Tailscale state, not
pairing or reaching a particular remote phone.
