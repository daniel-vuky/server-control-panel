---
id: E2-F06-US03
type: user-story
epic: Discover Projects
feature: Tracked Projects
status: Draft
personas:
  - Server Owner
source: "Server Control Panel – Product Spec & E2E Flow — §4.3 Settings, §6 Not found"
---
# E2-F06-US03 — Stop Tracking a Project

## User Story

As the Server Owner, I want to remove a project from the panel, so that projects I no longer manage do not clutter it.

## Acceptance Criteria

### Scenario: Stopping tracking from a project's settings

- **Given** a tracked project
- **When** I choose **Stop tracking**
- **Then** I am told the project will leave the panel and that nothing on the server or on Google Drive will be touched
- **And** I must confirm before it happens

### Scenario: Nothing on the server changes

- **Given** I confirmed stopping tracking
- **When** the project is removed
- **Then** its containers or processes keep their current state
- **And** its files and its backups on Google Drive are left as they are
- **And** its scheduled backups no longer run

### Scenario: Removing a project that is not found

- **Given** a project marked **Not found**
- **When** I choose **Remove from list**
- **Then** it leaves the panel

### Scenario: History is kept

- **Given** a project I stopped tracking
- **When** I read the activity log
- **Then** its earlier entries are still there
