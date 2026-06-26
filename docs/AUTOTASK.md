# Autotask setup & permissions

How to wire `atem` to an Autotask instance, the least-privilege API permissions
it needs, and the ticket/time-entry field requirements — all confirmed against a
live instance.

## API user & credentials

atem authenticates with three values from an **API User (API-only)** resource:

- **UserName** and **Secret** (the generated password)
- an **API Integration Code** (the "API Tracking Identifier") — a separate value,
  not the username

Set them in `atem ui` (local panel) or via environment variables
(`ATEM_USERNAME`, `ATEM_SECRET`, `ATEM_INTEGRATION_CODE`). The API zone URL is
auto-detected from the username and cached in the config file.

## Least-privilege security level

atem performs **no deletes** and touches only these entities:

| Entity | Read | Add | Edit | Used by |
|---|:--:|:--:|:--:|---|
| Companies / Organizations | ✅ | | | `company search` |
| Tickets | ✅ | ✅ | ✅ | ticket search/show/create/close, timer, report |
| TimeEntries | ✅ | ✅ | | report, logging time |
| BillingCodes | ✅ | | | `config doctor` (work types) |
| Resources | ✅ *(optional)* | | | `resource search` only |

Everything else: **None**. Delete: **None** everywhere.

- **Read-only user** (reports/lookups) = the Read column above.
- **Write user** (full timer/ticket flow) = Read **+ Tickets: Add & Edit + TimeEntries: Add**.

Observed live: a tightly-scoped read-only user could read Companies, Tickets,
TimeEntries and BillingCodes, but was **denied Resources** — that's expected and
fine (atem reads your `resourceId`/`roleId` off existing time entries instead).

## Required config for the write flow

Set via `atem ui` or `atem config set`:

| Key | Meaning |
|---|---|
| `resourceId` | your technician resource — time is logged as this |
| `roleId` | your billing role — **required** on ticket time entries |
| `queueId` | default queue for new tickets |
| `ticketStatusNew` / `ticketStatusComplete` | status ids (e.g. Ongoing / Complete) |
| `billingCodeId` | **optional** work type — see below |

Discover the ids with `atem config doctor` (lists queues, statuses, priorities,
work types). Your `resourceId` and `roleId` can be read off your existing time
entries — `atem report --match <keyword>` exposes `resourceId`/`roleId` in the
JSON (never the customer markdown).

## Field requirements (confirmed live)

**Creating a ticket** — atem sets the ticket's **Primary Resource (Role)**
(`assignedResourceID` + `assignedResourceRoleID`) from your `resourceId`/`roleId`,
so the work is assigned to you for follow-up.

**Logging time on a (service) ticket** requires:

- `resourceID` (you) — works directly; **no `ImpersonationResourceId` needed**.
- `roleID` — **required**, or Autotask rejects with *"must have a roleID"*.
- `summaryNotes` — **required (non-blank)**, or Autotask rejects with *"TimeEntry.summaryNotes can not be blank."* Log with `timer stop --note "..."` (or accumulate notes on the session first).
- `startDateTime` + `endDateTime` — **required for service tickets**; atem derives
  a window whose length matches the logged hours, ending now by default or — with
  `timer stop --date YYYY-MM-DD` — at the end of that worked business day.
  Hosted/container runs should set `ATEM_TIMEZONE` (for this deployment,
  `Europe/Stockholm`) so relative dates and clock windows such as `08-09` are
  interpreted as local work time before atem sends UTC timestamps to Autotask.
- work type / allocation code (`billingCodeID`) — the write user is typically
  **not authorized to set it**. Either:
  - leave `billingCodeId` **unset** → Autotask uses the ticket/contract default
    (recommended, and what this setup uses), or
  - grant the security level permission to set allocation codes on time entries,
    then set `billingCodeId`.

## Other notes

- **Company id `0` is valid** — it's the owner organization. atem treats it as a
  real company id (it is not a "missing company" sentinel).
- atem **never deletes**. Verification/test tickets stay (closed); remove them in
  the Autotask portal if you want them gone.
- All writes support `--dry-run` to preview the exact payload first.
