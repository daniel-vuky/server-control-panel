---
id: E2-F07-US01
type: user-story
epic: Discover Projects
feature: Onboarding
status: Draft
personas:
  - Server Owner
source: "Server Control Panel – Product Spec & E2E Flow — §4.1, Flow A step 3, Flow B"
---
# E2-F07-US01 — Complete First-Run Setup

## User Story

As the Server Owner, I want a short guided setup the first time I open the panel, so that I go from nothing to a working Dashboard in one sitting.

## Acceptance Criteria

### Scenario: Setup is laid out as four steps

- **Given** I am in first-run setup
- **When** I look at it
- **Then** I see four steps — Workspace, Scan results, Google Drive, Done
- **And** the step I am on and the steps already done are marked

### Scenario: Workspace and scan results

- **Given** I am on the first step
- **When** I add a workspace and the scan finishes
- **Then** I move to scan results, following [[E2-F04-US01 - Add a Workspace]] and [[E2-F06-US01 - Choose Which Scanned Projects to Track]]

### Scenario: Connecting Google Drive or leaving it for later

- **Given** I have added projects
- **When** I reach the Google Drive step
- **Then** I can connect it, following [[E5-F14-US01 - Connect Google Drive]]
- **And** I can choose **Later** and still finish setup

### Scenario: Finishing

- **Given** I reach the last step
- **When** I see it
- **Then** I am told how many projects were added
- **And** **Go to Dashboard** takes me there

### Scenario: Going back to rescan

- **Given** I am reviewing scan results
- **When** I choose **Rescan**
- **Then** I return to the workspace step with my path and depth kept
