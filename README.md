# atem — AutoTask EasyMode

A terminal-friendly, **AI-driveable** wrapper around the Autotask PSA REST API.

`atem` does two things:

1. Keeps **loose local work timers** — start one when you begin on a customer,
   pause/switch as you jump between tasks, stop when you're done. You don't have
   to log to the second; the timer produces a *suggested* number of hours that
   you confirm or override.
2. Turns that into **Autotask tickets, time entries, and reports** through a
   read-only-friendly JSON interface that an AI agent (e.g. Claude Code) can call
   on your behalf.

Every command prints a single JSON object, and everything that writes to
Autotask supports `--dry-run`.

## The workflow it's built for

You tell your AI assistant what you're doing in plain language; it runs `atem`:

> **You:** "Jag börjar med Acme Corp nu och gör deras script som mappar upp
> importfält i Excel."

```console
$ atem timer start --company acme --title "Script: mappa importfält (Excel)"
```

…creates a ticket on the right customer and starts the timer. Later:

> **You:** "Nu är jag klar — det blev ungefär 2 timmar, fick ihop mappningen men
> fick bråka med datumformaten."

```console
$ atem timer stop --hours 2 --close \
    --note "Byggde Excel-mappning för importfält; löste datumformat-strul."
```

…logs the time entry on the ticket and closes it. And when the manager asks:

> **You:** "Kunden vill ha en sammanfattning av allt jag gjort i projektet."

```console
$ atem report --company acme --from 2026-02-01 --to 2026-05-31 --format md
```

…pulls every ticket + time entry + note for that customer and hands back
structured JSON (plus a Markdown summary) ready to drop into the customer's AI.

## Build

Requires Go 1.23+ (developed on 1.26).

```console
$ go build -o atem ./        # or: go install ./...
```

## Configure

You need an Autotask API user **with write permission** (a read-only user can
only run lookups and reports).

Credentials resolve from environment variables first, then the config file.
Prefer env vars for the secret:

```pwsh
$env:ATEM_USERNAME = "yourapiuser@example.com"
$env:ATEM_SECRET = "••••••••"
$env:ATEM_INTEGRATION_CODE = "YOURINTEGRATIONCODE"
```

Then set the org-specific defaults (these differ per Autotask instance). Run
`atem config doctor` first — it verifies the credentials and zone and lists your
actual status / priority / queue / work-type IDs so you don't have to guess:

```console
$ atem config doctor                        # verify + list your org's IDs
$ atem resource search "Alex Example"       # find YOUR resourceId (who time is logged as)
$ atem config set resourceId 12345

$ atem config set queueId 8                  # default queue for new tickets
$ atem config set billingCodeId 14           # work type for time entries
$ atem config set ticketStatusNew 1
$ atem config set ticketStatusComplete 5

$ atem company search "Acme"                 # find a companyID...
$ atem company alias acme 0                   # ...and save a friendly alias
$ atem config show                            # review everything (secrets redacted)
```

Prefer a GUI? `atem ui` opens a local web panel (127.0.0.1) to edit and verify
the same config — with a show/hide toggle for the secret, and a **Verify** button
that runs `config doctor` and loads your org's queue/status/work-type IDs into
pick-lists. Nothing leaves your machine.

Config and timer state live in `%APPDATA%\atem\` (Windows) /
`~/.config/atem/` (Linux/macOS).

For the least-privilege API permissions and the exact ticket/time-entry field
requirements (role id, start/stop times, work-type handling, company id `0`),
see **[docs/AUTOTASK.md](docs/AUTOTASK.md)**.

## Commands

```
config   doctor | show | set <key> <value>
company  search <query> | alias <name> <companyID>
resource search <name|email>
ticket   search [--company <a|id>] <text> [--limit N]
         create --company <a|id> --title "..." [--desc] [--dry-run]
         show <id>
         close <id> [--dry-run]
timer    start --company <a|id> [--title] [--desc] [--ticket <id>] [--no-ticket]
               [--note] [--keep-others] [--dry-run]
         status
         note [session] <text>
         pause | resume | switch [session]
         stop [session] [--hours X] [--date YYYY-MM-DD] [--note "..."] [--close] [--dry-run]
time     add (--ticket <id> | --company <a|id>) [--title] [--desc]
             --windows "11-12,13-15" [--date YYYY-MM-DD] [--note] [--close] [--dry-run]
report   [--company <a|id>] [--match <text>] [--ticket <id>]
         [--from YYYY-MM-DD] [--to YYYY-MM-DD] [--format json|md] [--limit N] [--out FILE]
ui       [--port N] [--no-open]    open a local config panel in your browser
describe                          JSON of every command/flag (agent self-description)
mcp                               run as an MCP server over stdio (tools = commands)
help | version
```

Notes:
- A timer is a local session (`s1`, `s2`, …). Multiple can be open; only running
  ones accrue time, so `pause`/`switch` lets you jump between customers.
- `timer stop` uses the measured elapsed time unless you pass `--hours`; pass
  `--date YYYY-MM-DD` to backdate the entry.
- `time add` logs work you didn't run a live timer for. Each `--windows` range
  becomes its own time entry with real start/end times, so split work like
  `--windows "11-12,13-15"` is recorded as the actual windows (1 h + 2 h) instead
  of one merged 3 h block. Per-window notes via `--windows "11-12=did X,13-15=did Y"`;
  otherwise `--note` is applied to each.
- Use `--dry-run` on any write to preview the exact payload first.
- `report --match <keyword>` finds tickets by title keyword (across the whole
  account, or a single `--company`) and aggregates them in one call — ideal for a
  project summary, e.g. `atem report --match migration --format md`.
- `--out report.md` saves the report to a file (markdown when `--format md`).
  Report output contains customer data — write it outside this repo (or keep it
  gitignored); never commit it.
- `report` also returns a `flagged` list (JSON only, never the markdown) of
  entries worth itemizing before sending to a customer, each with a `reason`:
  `thin` (over `flagHoursOver` h — default 5 — with a note under `flagNotesUnder`
  chars — default 80) or `large` (at least `flagHoursAlways` h — default 12 —
  regardless of note). Tune with `atem config set flagHoursOver 8`,
  `flagNotesUnder 120`, `flagHoursAlways 16`.

## Driving atem from an AI agent (MCP)

`atem` is self-describing so an agent never has to guess the command surface:

- `atem describe` prints a JSON catalog of every command, flag (type, required,
  default), and example. No config needed.
- `atem mcp` runs an [MCP](https://modelcontextprotocol.io) server over stdio,
  exposing each command as a tool with a typed input **and** output schema
  (read-only and destructive commands are annotated; tool results include
  `structuredContent`). It also serves **resources** (`atem://describe`,
  `atem://config`) and **prompts** (`log_day`, `weekly_report`) that bake in
  atem's conventions. Register it once and the tools appear to the agent
  automatically:

  ```json
  { "mcpServers": { "atem": { "command": "atem", "args": ["mcp"] } } }
  ```

  Use the absolute path to the built binary if `atem` isn't on `PATH` (e.g.
  `C:\\path\\to\\atem.exe`). For a Claude Code project, this goes in a `.mcp.json`
  at the repo root — which is gitignored here because the path is machine-specific.
  The server runs the built binary, so rebuild (`go build -o atem.exe .`) after
  changing commands for the tools to reflect them.

`describe`, the MCP tool list, and even `atem help` are all generated from the
same command registry as the CLI dispatch; output schemas are generated by
reflection from the result structs the handlers return. So nothing in the
agent-facing surface can drift from what `atem` actually does. MCP tool calls run
the same handlers (and the same `--dry-run`/write-guards) as the CLI.

## Safety

`atem` writes real, billable data to your customer's PSA. Writes are explicit
and previewable: every create/close command accepts `--dry-run`, which prints
what *would* be sent without calling the API or changing local state.

## Development

```pwsh
./scripts/check.ps1          # full gate: build, vet, strict lint, tests + coverage
./scripts/check.ps1 -Fix     # auto-apply gofumpt formatting, then run the gate
```

```console
$ make check                 # same gate on Linux/macOS (CI also enables -race)
```

Code standard and architecture are documented in [AGENTS.md](AGENTS.md). The
linters are deliberately strict (`.golangci.yml`) as a guardrail for both humans
and AI agents editing the code.

> If you publish this to a Git host, change the module path in `go.mod` from
> `autotask-easymode` to your repo URL (e.g. `github.com/you/autotask-easymode`)
> so imports group conventionally; update the `autotask-easymode/...` import
> paths to match.
