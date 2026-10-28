---
id: E2-F04-US01
type: user-story
epic: Discover Projects
feature: Workspaces
status: Draft
personas:
  - Server Owner
source: "Server Control Panel – Product Spec & E2E Flow — §4.1 step 1, Flow B steps 1–2"
---
# E2-F04-US01 — Add a Workspace

## User Story

As the Server Owner, I want to point the panel at the root directory where my projects live, so that it can find them without my moving or registering each one.

## Acceptance Criteria

### Scenario: Adding a workspace with a scan depth

- **Given** I am adding a workspace
- **When** I enter a root path, such as `/home/deploy/projects`, choose a scan depth of 1, 2 or 3 levels and choose **Scan**
- **Then** the path is checked against the allowed directories (`WORKSPACE_ROOTS`), following [[E1-F02-US02 - Confine Scanning to Allowed Directories]]
- **And** a path outside them is refused with "add it to WORKSPACE_ROOTS and re-run install.sh"
- **And** if it passes, scanning starts, following [[E2-F04-US02 - Follow Scan Progress]]

### Scenario: Depth decides what counts as a project folder

- **Given** a scan depth of N
- **When** the workspace is scanned
- **Then** folders up to N levels below the root are examined as possible projects
- **And** nothing deeper is examined

### Scenario: The workspace is remembered

- **Given** a scan has finished and I have added its projects
- **When** I return later
- **Then** the workspace is stored with its name, path, scan depth and the time of its last scan

### Scenario: Projects are not moved or changed

- **Given** any workspace
- **When** it is added and scanned
- **Then** no file or folder in it is created, moved or changed by the panel
