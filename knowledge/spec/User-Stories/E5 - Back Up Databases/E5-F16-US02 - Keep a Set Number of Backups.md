---
id: E5-F16-US02
type: user-story
epic: Back Up Databases
feature: Scheduled Backup
status: Draft
personas:
  - Server Owner
source: "Server Control Panel – Product Spec & E2E Flow — §4.3 Database & Backup, Flow E step 4"
---
# E5-F16-US02 — Keep a Set Number of Backups

## User Story

As the Server Owner, I want only the newest N backups of each database kept on Drive, so that storage does not grow without limit.

## Acceptance Criteria

### Scenario: Setting how many to keep

- **Given** a database's backup settings
- **When** I set the number of copies to keep, such as 14, and save
- **Then** that number is stored for that database

### Scenario: Old copies are removed after a good backup

- **Given** a database already holds as many copies as it keeps
- **When** a new backup succeeds
- **Then** the oldest copies beyond that number are removed from Drive

### Scenario: A failed backup removes nothing

- **Given** a backup fails at any step
- **When** it ends
- **Then** no older copy is removed

### Scenario: Only the panel's own copies count

- **Given** files in the project's Drive folder that the panel did not create
- **When** old copies are removed
- **Then** those files are never counted or removed
