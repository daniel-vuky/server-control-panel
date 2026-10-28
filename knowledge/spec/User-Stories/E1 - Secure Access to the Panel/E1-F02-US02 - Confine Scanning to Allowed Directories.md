---
id: E1-F02-US02
type: user-story
epic: Secure Access to the Panel
feature: Command Safety
status: Draft
personas:
  - Server Owner
source: "Server Control Panel – Product Spec & E2E Flow — §3 Security 4, Flow B step 2"
---
# E1-F02-US02 — Confine Scanning to Allowed Directories

## User Story

As the Server Owner, I want scanning and file reading to stay inside the directories I have allowed, so that the panel cannot be pointed at the rest of the filesystem.

## Acceptance Criteria

### Scenario: A path outside the allowed directories is refused

- **Given** a path that is not inside any of the allowed directories, which are the `WORKSPACE_ROOTS` ([ADR 0008](../../../../docs/decisions/0008-deploy-the-panel-as-a-docker-compose-project-with-pull-based-updates.md))
- **When** I submit it as a workspace
- **Then** it is refused with a message under the field: "add it to WORKSPACE_ROOTS and re-run install.sh"
- **And** nothing is scanned

### Scenario: Paths that climb out are refused

- **Given** a path containing `..` that would resolve outside an allowed directory
- **When** I submit it
- **Then** it is refused in the same way

### Scenario: Symlinks that point outside are not followed

- **Given** a workspace containing a symlink to a location outside the allowed directories
- **When** the workspace is scanned
- **Then** the symlink is not followed
- **And** nothing behind it is read or listed

### Scenario: A path that does not exist or cannot be read

- **Given** a path that does not exist, or that the panel's user cannot read
- **When** I submit it
- **Then** I am told which of the two it is, under the field
- **And** nothing is scanned
