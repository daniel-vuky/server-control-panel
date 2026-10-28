---
id: E2-F06-US02
type: user-story
epic: Discover Projects
feature: Tracked Projects
status: Draft
personas:
  - Server Owner
source: "Server Control Panel – Product Spec & E2E Flow — §4.2 Rescan, Flow B step 8"
---
# E2-F06-US02 — Rescan for New and Missing Projects

## User Story

As the Server Owner, I want to rescan a workspace and see what has appeared and what has gone, so that the panel follows changes on the server without my starting over.

## Acceptance Criteria

### Scenario: Rescanning from the Dashboard

- **Given** I am on the Dashboard
- **When** I choose **Rescan**
- **Then** every workspace is scanned again with its saved depth

### Scenario: A new project appears

- **Given** a folder that was not there at the last scan is recognised as a project
- **When** the rescan finishes
- **Then** it is offered for tracking and marked **New**

### Scenario: A tracked project's folder is gone

- **Given** a tracked project whose folder no longer exists
- **When** the rescan finishes
- **Then** the project is marked **Not found**
- **And** it is not removed on its own
- **And** the only action it offers is **Remove from list**

### Scenario: Tracked projects keep what I set

- **Given** a tracked project I renamed
- **When** the workspace is rescanned
- **Then** my name is kept
