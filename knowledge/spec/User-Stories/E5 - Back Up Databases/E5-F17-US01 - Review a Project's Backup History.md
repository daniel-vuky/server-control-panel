---
id: E5-F17-US01
type: user-story
epic: Back Up Databases
feature: Backup Records
status: Draft
personas:
  - Operator
source: "Server Control Panel – Product Spec & E2E Flow — §4.3 Database & Backup"
---
# E5-F17-US01 — Review a Project's Backup History

## User Story

As an Operator, I want every backup of a project listed with its outcome and its file, so that I can find the copy I need and see whether backups are healthy.

## Acceptance Criteria

### Scenario: The history table

- **Given** a project's Database & Backup tab
- **When** I look at its history
- **Then** each backup is a row with its time, size, duration, result and what started it — a person's email or the schedule
- **And** the newest is first

### Scenario: Reaching the file

- **Given** a successful backup row
- **When** I look at its actions
- **Then** I can open it on Google Drive or download it

### Scenario: A failed backup row

- **Given** a failed backup row
- **When** I look at it
- **Then** it states the step that failed
- **And** it offers **Retry** in place of the file links

### Scenario: No backup yet

- **Given** a database never backed up
- **When** I open its history
- **Then** I am told there is no backup yet and offered **Back up now**
