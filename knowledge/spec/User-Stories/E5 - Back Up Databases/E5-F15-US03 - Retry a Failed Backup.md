---
id: E5-F15-US03
type: user-story
epic: Back Up Databases
feature: Manual Backup
status: Draft
personas:
  - Operator
source: "Server Control Panel – Product Spec & E2E Flow — Flow E step 5, §6 Common errors"
---
# E5-F15-US03 — Retry a Failed Backup

## User Story

As an Operator, I want a failed backup to point at the step that failed and let me try again, so that I can fix the cause and get a good copy.

## Acceptance Criteria

### Scenario: The failed step is marked

- **Given** a backup fails
- **When** I look at its progress
- **Then** the step that failed is shown in red with its error
- **And** the steps after it are shown as skipped

### Scenario: A wrong database password

- **Given** the dump fails because the database password is wrong
- **When** the error is shown
- **Then** it is reported at the Dump step
- **And** it suggests checking the environment variable it came from, without showing its value

### Scenario: Nothing is left behind

- **Given** a backup failed
- **When** it ends
- **Then** temporary files on the server are removed
- **And** existing copies on Drive are not touched

### Scenario: Trying again

- **Given** a failed backup
- **When** I choose **Retry**
- **Then** a new backup of the same database starts from the first step

### Scenario: The failure is recorded

- **Given** a failed backup
- **When** I look at the backup history and the activity log
- **Then** both show it as failed, with the step and the reason
