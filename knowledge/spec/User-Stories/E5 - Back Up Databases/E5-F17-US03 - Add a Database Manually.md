---
id: E5-F17-US03
type: user-story
epic: Back Up Databases
feature: Backup Records
status: Draft
personas:
  - Server Owner
source: "Server Control Panel – Product Spec & E2E Flow — §6 Empty states"
---
# E5-F17-US03 — Add a Database Manually

## User Story

As the Server Owner, I want to add a database the scan did not find, so that it can be backed up like the others.

## Acceptance Criteria

### Scenario: Offered when nothing was found

- **Given** a project where no database was detected
- **When** I open its Database & Backup tab
- **Then** I am told no database was detected
- **And** I am offered **Add database manually**

### Scenario: Describing the database

- **Given** I add a database by hand
- **When** I fill it in
- **Then** I give its type, where it runs — a container of the project or the host — its name, and which environment variable holds its credentials
- **And** I never type a password into the panel

### Scenario: Checking it before saving

- **Given** I have described a database
- **When** I save
- **Then** the panel checks it can connect
- **And** if it cannot, I am told why and it is not saved

### Scenario: Once added

- **Given** a database added by hand
- **When** I look at it
- **Then** it can be backed up and scheduled like a detected one
- **And** it is marked as added by hand
