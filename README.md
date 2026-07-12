# ATEM - AutoTask EasyMode

ATEM is an Autotask PSA MCP gateway for Microsoft 365 Copilot and local AI
agents.

The `atem` binary is intentionally still a command-line program, but the CLI is
not the product. It is the local runner used for setup, debugging, tests, and
agent fallback. The primary runtime is:

```pwsh
atem serve --addr :8080 --toolset m365 --auth entra
```

That serves a hosted MCP endpoint for Copilot-style clients. Every handler keeps
the same JSON result contract, and every Autotask write supports `--dry-run`, so
the hosted path and the local runner execute the same business logic.

## Intended Shape

ATEM has three surfaces:

| Surface | Purpose |
|---|---|
| Hosted MCP, `atem serve` | Primary app surface for Microsoft 365 Copilot and other remote MCP clients. |
| Local MCP, `atem mcp` | Development and local-agent integration over stdio. |
| Local JSON runner, `atem <command>` | Setup, smoke tests, support/debugging, and fallback when MCP tools are unavailable. |

The command registry in `registry.go` is the source of truth for all handler
metadata. Each handler declares whether it belongs to the local runner, the
Copilot-safe hosted surface, or both. `atem describe`, MCP `tools/list`, and
`atem help` are generated from that registry.

## Copilot Workflow

A technician should be able to describe work in plain language and let the agent
use ATEM tools to:

- find the right Autotask company and contact;
- find or create the ticket;
- classify new tickets with issue/sub-issue types when the context is clear;
- log explicit time windows as separate Autotask time entries;
- create customer/project reports from Autotask ground truth.

The hosted `m365` toolset is deliberately smaller than the local runner. It
exposes:

- `company search`
- `contact search`
- `contact create`
- `ticket search`
- `ticket issue-types`
- `ticket show`
- `ticket create`
- `time add`
- `report`

It excludes local/admin/debug flows such as config editing, local aliases,
resource lookup, local timers, and ticket close until additional server-side
policy checks exist.

## Hosted MCP

Run the server locally for MCP transport testing:

```pwsh
atem serve --addr 127.0.0.1:8080 --toolset m365 --auth none
```

Production-style hosted MCP should require Entra-authenticated profiles:

```pwsh
atem serve `
  --addr :8080 `
  --toolset m365 `
  --auth entra `
  --tenant-id <tenant-guid> `
  --audience <api-client-id-or-app-id-uri>
```

The `/mcp` endpoint accepts Streamable HTTP-style JSON-RPC POSTs. `/healthz`
returns a small JSON health response for container probes.

Container builds default to hosted M365 mode:

```pwsh
docker build -t atem-mcp .
docker run --rm -p 8080:8080 atem-mcp
```

For Azure Container Apps deployment, GitHub OIDC, Key Vault secret wiring, and
Copilot OAuth setup, see [docs/AZURE_DEPLOY.md](docs/AZURE_DEPLOY.md). For the
architecture notes and remaining policy decisions, see
[docs/M365_COPILOT.md](docs/M365_COPILOT.md).

## Local Setup

Requires Go 1.23+.

Install the runnable `atem` binary into the Go bin directory on PATH:

```pwsh
./scripts/install.ps1
atem version
```

For ad hoc development builds, `go build ./...` is enough for verification, but
use `./scripts/install.ps1` after changes that you want to run through `atem`.
The install script stamps the binary with git commit and build time.

Credentials resolve from environment variables first, then the 0600 config file:

```pwsh
$env:ATEM_USERNAME = "yourapiuser@example.com"
$env:ATEM_SECRET = "<secret>"
$env:ATEM_INTEGRATION_CODE = "<integration-code>"
```

Then set org-specific defaults. These IDs differ per Autotask instance:

```pwsh
atem config doctor
atem resource search "Alex Example"
atem config set resourceId 12345
atem config set roleId 67890
atem config set queueId 8
atem config set ticketStatusNew 1
atem config set ticketStatusComplete 5
atem config show
```

Config and timer state live in `%APPDATA%\atem\` on Windows and
`~/.config/atem/` on Linux/macOS.

For least-privilege Autotask API permissions and field requirements, see
[docs/AUTOTASK.md](docs/AUTOTASK.md).

## Local Runner

The local runner is mainly for debugging the same handlers exposed through MCP:

```pwsh
atem describe
atem help
atem company search "Acme"
atem ticket create --company 0 --title "Review" --desc "What the work is about" --dry-run
atem time add --ticket 121159 --date 2026-06-16 --windows "11-12,13-15" --note "Work note" --dry-run
atem report --company 0 --from 2026-06-01 --to 2026-06-30 --format md
```

Important local-runner rules:

- Every command prints exactly one JSON object.
- A success result is `{ "ok": true, "action": "...", "data": ... }`.
- A failure result is `{ "ok": false, "error": "...", "hint": "..." }` with a
  non-zero exit code.
- Any command that writes to Autotask supports `--dry-run`.
- Split work must stay split: use `time add --windows "11-12,13-15"` instead of
  collapsing real clock windows into one merged entry.
- `report` returns a JSON-only `flagged` list for large/thin entries that should
  be reviewed before sending a customer-facing report.

`atem mcp` exposes the broad local surface over stdio for development agents. It
also serves resources (`atem://describe`, `atem://config`) and prompts
(`log_day`, `weekly_report`). A local project `.mcp.json` can point at the built
binary:

```json
{ "mcpServers": { "atem": { "command": "atem", "args": ["mcp"] } } }
```

Use an absolute path to the built binary if `atem` is not on PATH.

## Safety

ATEM writes real, billable data to Autotask. Hosted requests map the Entra user
to a server-side Autotask technician profile before injecting `resourceID`,
`roleID`, `assignedResourceID`, and `assignedResourceRoleID`. The model or user
does not supply those fields directly.

Writes should be previewed with `--dry-run` or an equivalent hosted confirmation
flow before committing. Keep tenant IDs, subscription IDs, FQDNs, profile JSON,
and Autotask credentials out of the repo.

## Development

```pwsh
./scripts/check.ps1          # build, vet, strict lint, tests + coverage
./scripts/check.ps1 -Fix     # apply gofumpt formatting, then run the gate
```

```console
$ make check                 # same gate on Linux/macOS; CI also enables -race
```

Code standards and repo-specific agent behavior are documented in
[AGENTS.md](AGENTS.md).

## License

Copyright (C) 2026 Gustaf Ekfeldt.

This project is licensed under the GNU Affero General Public License version 3
only (`AGPL-3.0-only`). See [LICENSE](LICENSE).
