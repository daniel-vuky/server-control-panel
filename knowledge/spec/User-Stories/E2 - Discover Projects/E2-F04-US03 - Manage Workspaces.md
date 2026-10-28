---
id: E2-F04-US03
type: user-story
epic: Discover Projects
feature: Workspaces
status: Draft
personas:
  - Server Owner
source: "Server Control Panel – Product Spec & E2E Flow — §4.7 Workspaces"
---
# E2-F04-US03 — Manage Workspaces

## User Story

As the Server Owner, I want to add, rescan and remove workspaces from Settings, so that the panel keeps matching how my server is organised.

## Acceptance Criteria

### Scenario: Seeing the workspaces

- **Given** one or more workspaces
- **When** I open Settings
- **Then** each workspace is listed with its path, scan depth, the number of projects tracked and ignored, and when it was last scanned

### Scenario: Adding another workspace

- **Given** I am in Settings
- **When** I choose **Add workspace**
- **Then** I add it as in [[E2-F04-US01 - Add a Workspace]]

### Scenario: Rescanning a workspace

- **Given** a workspace
- **When** I choose **Rescan**
- **Then** it is rescanned following [[E2-F06-US02 - Rescan for New and Missing Projects]]

### Scenario: Removing a workspace

- **Given** a workspace
- **When** I choose **Remove** and confirm
- **Then** the workspace and its tracked projects leave the panel
- **And** nothing on the server is stopped, deleted or changed
- **And** backups already on Google Drive stay where they are
