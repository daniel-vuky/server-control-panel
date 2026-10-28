---
id: E5-F15-US05
type: user-story
epic: Back Up Databases
feature: Manual Backup
status: Draft
personas:
  - Operator
source: "Server Control Panel – Product Spec & E2E Flow — §4.4"
---
# E5-F15-US05 — Back Up All Projects Now

## User Story

As an Operator, I want to back up every database in one action, so that I can take a full snapshot before server maintenance.

## Acceptance Criteria

### Scenario: Starting all backups

- **Given** Google Drive is connected
- **When** I choose **Back up all now** on Backups
- **Then** a backup of every detected database of every tracked project is queued

### Scenario: Backups run one at a time

- **Given** several backups are queued
- **When** they run
- **Then** they run one after another, so the server is not overloaded
- **And** I can see which is running and how many are left

### Scenario: One failure does not stop the rest

- **Given** one backup in the queue fails
- **When** it ends
- **Then** the next one still runs
- **And** at the end I am told how many succeeded and how many failed

### Scenario: Drive is not connected

- **Given** Google Drive is not connected or its token expired
- **When** I look at **Back up all now**
- **Then** it is disabled and the banner explains why
