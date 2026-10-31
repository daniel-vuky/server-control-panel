---
type: index
title: Server Control Panel — User Stories
status: Draft
source: "Server Control Panel – Product Spec & E2E Flow"
---
# Server Control Panel — User Stories

64 user stories in 8 epics and 26 features, covering the full v1 scope of the product spec: sections 1–7 and flows A–F. Each story lives in its epic's folder; IDs follow `E<epic>-F<feature>-US<story>`, with features numbered across the whole backlog.

v1 supports docker compose projects only. The architecture decisions behind these stories are recorded in [docs/decisions/](../../../docs/decisions/).

## Personas

- **Server Owner** — sets the panel up and decides how it behaves: workspaces, Google Drive, schedules, thresholds, alert channels.
- **Operator** — anyone on the Cloudflare Access allow list who watches and acts on projects day to day. The Server Owner is also an Operator.

v1 has no fine-grained permissions (spec §1, out of scope), so both personas can do everything. The persona on a story names whose need it serves, not who is allowed.

## Coverage of the E2E flows

| Flow | Stories |
|---|---|
| A — Sign in | E1-F01-US01, E1-F01-US02 |
| B — Add workspace and scan | E2-F04-US01, E2-F04-US02, E2-F05-US01, E2-F05-US02, E2-F06-US01, E2-F06-US02 |
| C — Status and resources | E3-F09-US01, E3-F11-US01, E3-F11-US02, E3-F11-US04, E6-F19-US01 |
| D — Start / Stop / Restart | E4-F12-US01 – US05, E1-F03-US01 |
| E — Manual backup to Google Drive | E5-F15-US01 – US03 |
| F — Connect Drive and schedule backups | E5-F14-US01, E5-F14-US02, E5-F16-US01, E7-F23-US02 |

## Additions beyond the spec — Accepted 2026-10-28

The spec does not say these, but a story needed a decision to be testable. Each was proposed and accepted on 2026-10-28:

1. Requests that reach the panel without a Cloudflare Access identity are refused (E1-F01-US01).
2. Secrets the panel knows are masked in errors and logs (E1-F02-US03).
3. Projects left unchecked at scan time are remembered as ignored and not offered again as new (E2-F06-US01).
4. Removing a workspace or stopping tracking never touches the server or Drive (E2-F04-US03, E2-F06-US03).
5. Charts show gaps, not zeros, where no data was sampled (E3-F11-US03).
6. Only one backup of the same database runs at a time; **Back up all now** runs backups one after another (E5-F15-US02, E5-F15-US05).
7. A database added by hand is checked by connecting before it is saved, and its password is never typed into the panel (E5-F17-US03).
8. Retention only ever deletes files the panel created (E5-F16-US02).
9. Alerts are sent once per incident, with a recovery message, and not for projects stopped on purpose (E7-F23-US01, E7-F23-US03).
10. Only scheduled backup failures send external alerts; manual failures are shown on screen (E7-F23-US02).
11. The panel lists its own resource usage in usage by app (E6-F20-US01).

Added on 2026-10-30 with the project lifecycle flow (#33, #39 to #44, #72), numbering on from the list above:

12. Restarting… is a third in-between status, next to Starting… and Stopping… (E4-F12-US03).
13. Restart of a Running or Partial project asks first (E4-F12-US03).
14. A start or restart that exits 0 but leaves the project in Error or Unknown is reported as failed. Partial counts as started (E4-F12-US01, E4-F12-US03).
15. A stop that exits 0 but leaves something running or restarting is reported as failed (E4-F12-US02).

Added on 2026-10-30 with the workspace management flow (#11, #19, #24, #25), numbering on from the list above:

16. A workspace whose root is missing, unreadable or empty, or that fails the workspace-roots check, marks no project Not found when it is rescanned; the rescan shows a warning for it instead (E2-F04-US03, E2-F05-US01).
17. Remove from list deletes the record of a project a rescan could not find, while Stop tracking on a present project remembers it as ignored (E2-F06-US03).
18. An ignored project can be tracked again from the rescan results, through Show ignored (E2-F06-US01).

Added on 2026-10-30 with the project detail flow (#31, #32, #34, #35, #37, #38), numbering on from the list above:

19. A project can stop with `up -d / down` instead of `up -d / stop`: `down` removes its containers and networks and keeps its volumes, and the project then reads Stopped (E3-F11-US05, E4-F12-US02).
20. Alert thresholds (CPU %, RAM MB, per project) are stored before alerts exist; the form says alerts are not sent yet, and the alerts flow will read them (E3-F11-US05, E7-F22-US02).
21. The log view opens with the 500 most recent lines and holds up to 2,000, dropping the oldest first; Download saves the lines held (E3-F11-US04).
22. At most 3 live log streams run at once, panel-wide; a fourth is refused with a message (E3-F11-US04).
23. An expired Cloudflare Access session is detected, and the connection badge offers Reload, because retrying cannot sign in again (E3-F09-US02).
24. A failed-components panel on Overview links, with View logs, to that container's most recent error in the logs (E3-F11-US02, E3-F11-US04).

Added on 2026-10-31 with the manual backup flow (#45, #46, #15, #48, #49, #50, #51, #56), numbering on from the list above:

25. The Drive status (not set up, not connected, connected, expired) is part of the live state, so every page shows a change at once and Back up now is offered or refused from it (E5-F14-US01, E5-F14-US03, E5-F15-US01).
26. A backup the panel refuses (Drive expired, no space, database not running) is kept in the history as Refused, with its reason, and can be retried (E5-F15-US03, E5-F15-US04, E5-F17-US01).
27. SQLite databases are not backed up yet: the app sees a path inside its own container, so copying it from the host is not reliable. Back up now says so (E5-F15-US01).
28. A dump has no total time limit. It fails after 5 minutes without a byte of output, with a safety ceiling of 6 hours (E5-F15-US01, E5-F15-US03).
29. The disk rule is best effort: the backup needs free space of at least 50 % of the database's data size plus 100 MiB. A dump that still outgrows the disk fails at the Dump step and its temporary file is removed (E5-F15-US04).
30. Remove old copies is shown as skipped ("No retention set yet") until retention exists, so every copy is kept (E5-F15-US02, E5-F16-US02).
31. A backup is downloaded through the panel, so any Operator on the Access list can fetch it without access to the Drive account; Open in Drive is offered next to it (E5-F17-US01).
32. The Google Cloud app must be published to Production. The `drive.file` scope needs no review, but a token issued while the app is in Testing expires after 7 days, and the panel then asks to reconnect (E5-F14-US01, E5-F14-US03).

Added on 2026-10-31 with the Dashboard flow (#27, #28, #29, #30, #73), numbering on from the list above:

33. In-flight projects stay visible under any status filter while counted under none, so a chip can read 0 above a card that is starting or stopping (E3-F08-US04).
34. The latest backup counts refused and interrupted backups as not OK (E3-F08-US02).
35. The tunnel tile treats Not running as down (E3-F08-US01).

<a id="open-questions"></a>
## Decisions (2026-10-28)

- **Restore** — out of scope for v1. The spec covers backing up only.
- **Stack support** — v1 supports docker compose projects only, so the question of how to stop and start projects without a process manager no longer applies ([ADR 5](../../../docs/decisions/0005-run-only-whitelisted-docker-compose-commands-without-a-shell.md)).
- **Encryption** — removed from v1. Backups are not encrypted by the panel (E5-F16-US03).
- **Retention period** — a setting from 7 to 30 days, default 7 (E6-F20-US01).
- **Domain checks** — each public domain is checked every 60 seconds, and an alert is sent after 3 consecutive failures (E7-F23-US01).
- **Partial status** — **Back up** is allowed while a project is Partial, as long as the database's container is running (E3-F10-US01).
- **Allowed directories** — the allowed directories are the `WORKSPACE_ROOTS` of the install ([ADR 8](../../../docs/decisions/0008-deploy-the-panel-as-a-docker-compose-project-with-pull-based-updates.md)); a path outside them is refused (E1-F02-US02, E2-F04-US01).

## Stories

## E1 — Secure Access to the Panel (7 stories)

**F01 — Sign-in**

- [[E1-F01-US01 - Sign In Through Cloudflare Access]] · Operator
- [[E1-F01-US02 - Land on the Right First Screen]] · Operator

**F02 — Command Safety**

- [[E1-F02-US01 - Run Only Whitelisted Commands]] · Server Owner
- [[E1-F02-US02 - Confine Scanning to Allowed Directories]] · Server Owner
- [[E1-F02-US03 - Keep Secrets Out of the Interface]] · Operator
- [[E1-F02-US04 - Protect the Google Drive Token]] · Server Owner

**F03 — Accountability**

- [[E1-F03-US01 - Attribute Every Action to a Person]] · Server Owner


## E2 — Discover Projects (9 stories)

**F04 — Workspaces**

- [[E2-F04-US01 - Add a Workspace]] · Server Owner
- [[E2-F04-US02 - Follow Scan Progress]] · Server Owner
- [[E2-F04-US03 - Manage Workspaces]] · Server Owner

**F05 — Project and Database Detection**

- [[E2-F05-US01 - Detect Compose Projects]] · Server Owner
- [[E2-F05-US02 - Detect the Databases a Project Uses]] · Server Owner

**F06 — Tracked Projects**

- [[E2-F06-US01 - Choose Which Scanned Projects to Track]] · Server Owner
- [[E2-F06-US02 - Rescan for New and Missing Projects]] · Server Owner
- [[E2-F06-US03 - Stop Tracking a Project]] · Server Owner

**F07 — Onboarding**

- [[E2-F07-US01 - Complete First-Run Setup]] · Server Owner


## E3 — Monitor Projects (12 stories)

**F08 — Dashboard**

- [[E3-F08-US01 - See Server Health at a Glance]] · Operator
- [[E3-F08-US02 - See Project Counts and the Latest Backup]] · Operator
- [[E3-F08-US03 - Browse Projects as Cards]] · Operator
- [[E3-F08-US04 - Filter, Search and Switch Views]] · Operator

**F09 — Live Updates**

- [[E3-F09-US01 - Receive Live Status and Metrics]] · Operator
- [[E3-F09-US02 - Know When Live Updates Are Lost]] · Operator

**F10 — Project Status**

- [[E3-F10-US01 - Show One Consistent Status per Project]] · Operator

**F11 — Project Detail**

- [[E3-F11-US01 - Open a Project's Detail]] · Operator
- [[E3-F11-US02 - Inspect a Project's Components]] · Operator
- [[E3-F11-US03 - Chart a Project's Resource Usage]] · Operator
- [[E3-F11-US04 - Read Live Logs]] · Operator
- [[E3-F11-US05 - Edit Project Settings]] · Server Owner


## E4 — Control Projects (6 stories)

**F12 — Lifecycle Actions**

- [[E4-F12-US01 - Start a Project]] · Operator
- [[E4-F12-US02 - Stop a Project]] · Operator
- [[E4-F12-US03 - Restart a Project]] · Operator
- [[E4-F12-US04 - Lock a Project While a Command Runs]] · Operator
- [[E4-F12-US05 - Handle a Failed or Timed-Out Command]] · Operator

**F13 — Runtime Availability**

- [[E4-F13-US01 - Lock Controls When Docker Is Unresponsive]] · Operator


## E5 — Back Up Databases (14 stories)

**F14 — Google Drive Connection**

- [[E5-F14-US01 - Connect Google Drive]] · Server Owner
- [[E5-F14-US02 - Test the Drive Connection]] · Server Owner
- [[E5-F14-US03 - Reconnect or Disconnect Google Drive]] · Server Owner

**F15 — Manual Backup**

- [[E5-F15-US01 - Back Up a Database Now]] · Operator
- [[E5-F15-US02 - Follow Backup Progress Step by Step]] · Operator
- [[E5-F15-US03 - Retry a Failed Backup]] · Operator
- [[E5-F15-US04 - Block a Backup When Disk Space Is Short]] · Operator
- [[E5-F15-US05 - Back Up All Projects Now]] · Operator

**F16 — Scheduled Backup**

- [[E5-F16-US01 - Schedule Backups for a Database]] · Server Owner
- [[E5-F16-US02 - Keep a Set Number of Backups]] · Server Owner
- [[E5-F16-US03 - Set Backup Defaults]] · Server Owner

**F17 — Backup Records**

- [[E5-F17-US01 - Review a Project's Backup History]] · Operator
- [[E5-F17-US02 - Review All Backups]] · Operator
- [[E5-F17-US03 - Add a Database Manually]] · Server Owner


## E6 — Monitor Server Resources (5 stories)

**F18 — Server Metrics**

- [[E6-F18-US01 - Chart Server Resources Over Time]] · Operator
- [[E6-F18-US02 - View System Information]] · Operator
- [[E6-F18-US03 - Check System Services]] · Operator

**F19 — Usage by App**

- [[E6-F19-US01 - Rank Apps by Resource Usage]] · Operator

**F20 — Metrics History**

- [[E6-F20-US01 - Sample and Retain Metrics]] · Server Owner


## E7 — Track Activity and Alerts (7 stories)

**F21 — Activity Log**

- [[E7-F21-US01 - Review the Activity Timeline]] · Operator

**F22 — Alert Configuration**

- [[E7-F22-US01 - Configure Alert Channels]] · Server Owner
- [[E7-F22-US02 - Set Alert Thresholds]] · Server Owner

**F23 — Alert Triggers**

- [[E7-F23-US01 - Alert When a Project Fails or Its Domain Stops Responding]] · Operator
- [[E7-F23-US02 - Alert When a Scheduled Backup Fails]] · Server Owner
- [[E7-F23-US03 - Alert When Resources Stay Over Threshold]] · Server Owner
- [[E7-F23-US04 - Alert When the Cloudflare Tunnel Disconnects]] · Server Owner


## E8 — Provide Consistent Interface Behavior (4 stories)

**F24 — Screen States**

- [[E8-F24-US01 - Show Loading, Empty and Error States on Every Screen]] · Operator

**F25 — Safe Interaction**

- [[E8-F25-US01 - Confirm Dangerous Actions]] · Operator
- [[E8-F25-US02 - Keep Live Numbers Stable]] · Operator

**F26 — Mobile Use**

- [[E8-F26-US01 - Check and Control Projects on a Phone]] · Operator
