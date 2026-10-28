---
id: E5-F16-US03
type: user-story
epic: Back Up Databases
feature: Scheduled Backup
status: Draft
personas:
  - Server Owner
source: "Server Control Panel – Product Spec & E2E Flow — §4.7 Backup defaults"
---
# E5-F16-US03 — Set Backup Defaults

## User Story

As the Server Owner, I want defaults for schedule, retention and compression, so that each new database starts with sensible backup settings.

## Acceptance Criteria

### Scenario: The defaults

- **Given** Settings
- **When** I open backup defaults
- **Then** I can set the schedule, the time, the number of copies to keep, and whether to compress

### Scenario: Defaults apply to new databases only

- **Given** I change the defaults
- **When** I save
- **Then** databases found from then on start with them
- **And** databases that already have their own settings are not changed
