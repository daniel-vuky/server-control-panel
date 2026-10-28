---
id: E5-F15-US04
type: user-story
epic: Back Up Databases
feature: Manual Backup
status: Draft
personas:
  - Operator
source: "Server Control Panel – Product Spec & E2E Flow — §6 Common errors"
---
# E5-F15-US04 — Block a Backup When Disk Space Is Short

## User Story

As an Operator, I want a backup refused up front when the server lacks room for the dump, so that a backup never fills the disk and takes my sites down.

## Acceptance Criteria

### Scenario: Not enough free space

- **Given** the free disk space is less than the dump is expected to need
- **When** a backup is about to start, by hand or on schedule
- **Then** it does not start
- **And** I am told how much space it needs and how much is free

### Scenario: Pointing to where space is used

- **Given** a backup was refused for space
- **When** I read the message
- **Then** it links to the usage by app on Server, following [[E6-F19-US01 - Rank Apps by Resource Usage]]

### Scenario: Refused scheduled backups alert

- **Given** a scheduled backup is refused for space
- **When** it is refused
- **Then** it counts as a failed scheduled backup, following [[E7-F23-US02 - Alert When a Scheduled Backup Fails]]
