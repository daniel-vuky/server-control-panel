---
id: E5-F16-US01
type: user-story
epic: Back Up Databases
feature: Scheduled Backup
status: Draft
personas:
  - Server Owner
source: "Server Control Panel – Product Spec & E2E Flow — §4.3 Database & Backup, Flow F steps 3–4"
---
# E5-F16-US01 — Schedule Backups for a Database

## User Story

As the Server Owner, I want each database backed up on a schedule I set, so that I always have recent copies without remembering to make them.

## Acceptance Criteria

### Scenario: Choosing a schedule

- **Given** a database in a project's Database & Backup tab
- **When** I choose Off, Daily or Weekly, and a time — and for Weekly a day
- **And** I choose **Save**
- **Then** the schedule is stored
- **And** I see when the next backup will run

### Scenario: A scheduled backup runs

- **Given** a database with a schedule
- **When** its time comes
- **Then** a backup runs exactly as in [[E5-F15-US01 - Back Up a Database Now]]
- **And** it is recorded as started by the schedule

### Scenario: A scheduled backup fails

- **Given** a scheduled backup
- **When** it fails at any step
- **Then** an alert is sent, following [[E7-F23-US02 - Alert When a Scheduled Backup Fails]]

### Scenario: Turning a schedule off

- **Given** a database with a schedule
- **When** I set it to Off and save
- **Then** no more scheduled backups of it run
- **And** existing copies are kept

### Scenario: Schedules use the server's time

- **Given** any schedule
- **When** its time is shown or applied
- **Then** it is in the server's time zone, and the time zone is stated
