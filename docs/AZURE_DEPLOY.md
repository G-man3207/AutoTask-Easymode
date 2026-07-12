# Azure / hosted notes

**Agents must not deploy to production from this repository.** There is no
in-repo production deploy path (no deploy workflow, no bootstrap deploy script).

Hosted Copilot / Entra OAuth configuration notes that are not deployment
procedures may live elsewhere (for example `docs/M365_COPILOT.md` and
`scripts/azure/set-token-lifetime.ps1` for access-token lifetime policy). Do not
use this file as a production rollout runbook.
