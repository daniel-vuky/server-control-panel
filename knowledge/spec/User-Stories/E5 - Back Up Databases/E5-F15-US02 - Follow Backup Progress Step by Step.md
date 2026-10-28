---
id: E5-F15-US02
type: user-story
epic: Back Up Databases
feature: Manual Backup
status: Draft
personas:
  - Operator
source: "Server Control Panel – Product Spec & E2E Flow — Flow E step 3, §4.4"
---
# E5-F15-US02 — Follow Backup Progress Step by Step

## User Story

As an Operator, I want to see which step a backup is on, so that I know it is moving and, if it fails, where.

## Acceptance Criteria

### Scenario: Four steps

- **Given** a backup is running
- **When** I watch it
- **Then** I see its steps in order — Dump, Compress, Upload to Drive, Remove old copies
- **And** each is marked as done, running or waiting

### Scenario: Detail per step

- **Given** a step is done or running
- **When** I look at it
- **Then** it shows what it did — such as the dump time, the size before and after compression, or how much has been uploaded

### Scenario: Progress stays visible across screens

- **Given** a backup is running
- **When** I go to Backups
- **Then** the running backup is shown there with the same steps

### Scenario: Only one backup per database at a time

- **Given** a backup of a database is running
- **When** anyone tries to back up the same database
- **Then** they are told one is already running and shown its progress
