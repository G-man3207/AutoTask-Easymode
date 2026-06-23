// Command atem (AutoTask EasyMode) is a terminal-friendly, AI-driveable wrapper
// around the Autotask PSA REST API. It keeps loose local work timers and turns
// natural technician workflows ("starting on customer X" / "done, ~2h") into
// tickets, time entries, and AI-friendly reports.
//
// Every command prints a single JSON object so the tool is easy to drive from
// an AI agent; writes to Autotask support --dry-run for a safe preview first.
package main

import (
	"fmt"
	"io"
	"os"
	"strings"
)

const version = "0.1.0"

// Build metadata, injected at link time via
// -ldflags "-X main.commit=<sha> -X main.buildTime=<ts>" (see scripts/install.ps1).
// They default to "unknown" for a plain `go build`/`go install`, which itself
// signals the binary was not built through the install script.
var (
	commit    = "unknown"
	buildTime = "unknown"
)

func main() {
	os.Exit(run(os.Args[1:]))
}

// run executes a command line and returns a process exit code. It is split from
// main so tests can exercise dispatch without spawning a process.
func run(args []string) int {
	if len(args) == 0 {
		printUsage(os.Stdout)
		return 2
	}
	switch args[0] {
	case "help", "--help", "-h":
		printUsage(os.Stdout)
		return 0
	case "version", "--version", "-v":
		_, _ = fmt.Fprintf(os.Stdout, "atem %s (commit %s, built %s)\n", version, commit, buildTime)
		return 0
	case "describe":
		// Self-description of every command/flag — no config needed, so an agent
		// can discover the surface before credentials are set.
		_ = writeJSON(os.Stdout, Result{OK: true, Action: "describe", Data: describeData()})
		return 0
	}

	app, err := newApp()
	if err != nil {
		_ = writeJSON(os.Stdout, resultFromError(err))
		return 1
	}
	if args[0] == "mcp" {
		// Long-running MCP server over stdio; it owns stdout, so don't wrap it.
		return app.serveMCP(os.Stdin, os.Stdout)
	}
	res, err := app.dispatch(args)
	if err != nil {
		_ = writeJSON(os.Stdout, resultFromError(err))
		return 1
	}
	_ = writeJSON(os.Stdout, Result{OK: true, Action: res.action, DryRun: res.dryRun, Data: res.data})
	return 0
}

// dispatch routes a command line to its handler using the registry. It matches
// the longest command name first ("ticket create"), then a single word
// ("report"), so the set of commands lives in exactly one place.
func (a *App) dispatch(args []string) (*cmdResult, error) {
	if len(args) >= 2 {
		if c := lookupCommand(args[0] + " " + args[1]); c != nil {
			return c.run(a, args[2:])
		}
	}
	if c := lookupCommand(args[0]); c != nil {
		return c.run(a, args[1:])
	}
	if subs := subcommandsOf(args[0]); len(subs) > 0 {
		sub := ""
		if len(args) >= 2 {
			sub = args[1]
		}
		return nil, hinted(args[0]+" subcommands: "+strings.Join(subs, ", "), "%s: unknown subcommand %q", args[0], sub)
	}
	return nil, hinted("run `atem help` to see commands", "unknown command %q", args[0])
}

func printUsage(w io.Writer) {
	_, _ = io.WriteString(w, usageText())
}
