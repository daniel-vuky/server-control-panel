---
id: E5-F17-US02
type: user-story
epic: Back Up Databases
feature: Backup Records
status: Draft
personas:
  - Operator
source: "Server Control Panel – Product Spec & E2E Flow — §4.4"
---
# E5-F17-US02 — Review All Backups

## User Story

As an Operator, I want one page of all backups across projects with a summary, so that I can check the health of backups for the whole server at once.

## Acceptance Criteria

### Scenario: The summary

- **Given** I open Backups
- **When** I look at the top
- **Then** I see the total size on Drive, the number of successful and failed backups in the last 7 days, and when the next scheduled backup runs and for which projects

### Scenario: All backups in one table

- **Given** Backups
- **When** I look at the table
- **Then** every backup of every project is a row with its time, project, database, size, duration, result, what started it and its file links

### Scenario: Filtering

- **Given** the table
- **When** I filter by project, by result or by a date range
- **Then** only matching rows are shown

### Scenario: A running backup

- **Given** a backup is running
- **When** I open Backups
- **Then** it is shown above the table with its steps, following [[E5-F15-US02 - Follow Backup Progress Step by Step]]

### Scenario: Drive not connected

- **Given** Google Drive is not connected
- **When** I open Backups
- **Then** a banner says so and offers **Connect Google Drive**
