---
id: E1-F01-US02
type: user-story
epic: Secure Access to the Panel
feature: Sign-in
status: Draft
personas:
  - Operator
source: "Server Control Panel – Product Spec & E2E Flow — Flow A step 3"
---
# E1-F01-US02 — Land on the Right First Screen

## User Story

As an Operator, I want to land on setup the first time and on the Dashboard afterwards, so that I am never shown an empty panel with nothing to do.

## Acceptance Criteria

### Scenario: First visit goes to setup

- **Given** no workspace has been added yet
- **When** I sign in
- **Then** I land on first-run setup, following [[E2-F07-US01 - Complete First-Run Setup]]

### Scenario: Later visits go to the Dashboard

- **Given** at least one workspace exists
- **When** I sign in
- **Then** I land on the Dashboard
- **And** setup is not shown again

### Scenario: Setup left unfinished

- **Given** a workspace was added but setup was left before the last step
- **When** I sign in again
- **Then** I land on the Dashboard with the projects that were added
- **And** anything skipped, such as Google Drive, stays reachable from Settings
