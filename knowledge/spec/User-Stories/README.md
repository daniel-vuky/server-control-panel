---
type: index
title: Server Control Panel — User Stories
status: Draft
source: "Server Control Panel – Product Spec & E2E Flow"
---
# Server Control Panel — User Stories

65 user stories in 8 epics and 26 features, covering the full v1 scope of the product spec: sections 1–7 and flows A–F. Each story lives in its epic's folder; IDs follow `E<epic>-F<feature>-US<story>`, with features numbered across the whole backlog.

## Personas

- **Server Owner** — sets the panel up and decides how it behaves: workspaces, Google Drive, schedules, thresholds, alert channels.
- **Operator** — anyone on the Cloudflare Access allow list who watches and acts on projects day to day. The Server Owner is also an Operator.

v1 has no fine-grained permissions (spec §1, out of scope), so both personas can do everything. The persona on a story names whose need it serves, not who is allowed.

## Coverage of the E2E flows

| Flow | Stories |
|---|---|
| A — Sign in | E1-F01-US01, E1-F01-US02 |
| B — Add workspace and scan | E2-F04-US01, E2-F04-US02, E2-F05-US01, E2-F05-US02, E2-F05-US03, E2-F06-US01, E2-F06-US02 |
| C — Status and resources | E3-F09-US01, E3-F11-US01, E3-F11-US02, E3-F11-US04, E6-F19-US01 |
| D — Start / Stop / Restart | E4-F12-US01 – US05, E1-F03-US01 |
| E — Manual backup to Google Drive | E5-F15-US01 – US03 |
| F — Connect Drive and schedule backups | E5-F14-US01, E5-F14-US02, E5-F16-US01, E7-F23-US02 |

## Additions beyond the spec — please confirm

The spec does not say these, but a story needed a decision to be testable. Each is a proposal:

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

## Open questions

- **Restore** — the spec covers backing up but not restoring. Is restore out of scope for v1?
- **Stopping `nextjs-node` projects** — with no process manager, which command stops and starts them?
- **Encryption** — where is the backup passphrase set and kept (E5-F16-US03)?
- **Retention period** — the spec says 7–30 days. Is it fixed or a setting?
- **Domain checks** — how often is each public domain checked for E7-F23-US01?
- **Partial status** — should **Back up** be allowed while a project is Partial? The spec's status table leaves it out.

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


## E2 — Discover Projects (10 stories)

**F04 — Workspaces**

- [[E2-F04-US01 - Add a Workspace]] · Server Owner
- [[E2-F04-US02 - Follow Scan Progress]] · Server Owner
- [[E2-F04-US03 - Manage Workspaces]] · Server Owner

**F05 — Stack and Database Detection**

- [[E2-F05-US01 - Detect the Stack of Each Project]] · Server Owner
- [[E2-F05-US02 - Detect the Databases a Project Uses]] · Server Owner
- [[E2-F05-US03 - Correct or Assign a Stack]] · Server Owner

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

- [[E4-F13-US01 - Lock Controls When Docker or pm2 Is Unresponsive]] · Operator


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
