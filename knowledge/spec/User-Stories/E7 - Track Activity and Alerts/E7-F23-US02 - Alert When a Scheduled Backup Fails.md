---
id: E7-F23-US02
type: user-story
epic: Track Activity and Alerts
feature: Alert Triggers
status: Draft
personas:
  - Server Owner
source: "Server Control Panel – Product Spec & E2E Flow — Flow F step 4, §6 External alerts"
---
# E7-F23-US02 — Alert When a Scheduled Backup Fails

## User Story

As the Server Owner, I want to be alerted when a scheduled backup fails, so that I never discover a missing backup on the day I need it.

## Acceptance Criteria

### Scenario: A scheduled backup fails

- **Given** a scheduled backup
- **When** it fails at any step, or is refused for lack of disk space
- **Then** an alert is sent through every enabled channel naming the project, the database, the step and the reason

### Scenario: It is recorded

- **Given** such an alert
- **When** I open Activity
- **Then** the failure and the alert are both there

### Scenario: Manual backups do not alert

- **Given** a backup started by a person
- **When** it fails
- **Then** the person is shown the failure on screen
- **And** no external alert is sent
