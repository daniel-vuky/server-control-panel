---
id: E2-F06-US01
type: user-story
epic: Discover Projects
feature: Tracked Projects
status: Draft
personas:
  - Server Owner
source: "Server Control Panel – Product Spec & E2E Flow — §4.1 step 2, Flow B steps 5–7"
---
# E2-F06-US01 — Choose Which Scanned Projects to Track

## User Story

As the Server Owner, I want to review what a scan found and pick what to track, so that the Dashboard holds only the projects I care about.

## Acceptance Criteria

### Scenario: Reviewing scan results

- **Given** a scan has finished
- **When** I see its results
- **Then** each project found is a row with a checkbox, its display name, its path, its stack, its current status and the databases detected
- **And** every row starts checked

### Scenario: Leaving a project out

- **Given** scan results
- **When** I uncheck a row
- **Then** it is not added
- **And** the count on the add button goes down by one

### Scenario: Renaming before adding

- **Given** a row in scan results
- **When** I change its display name
- **Then** it is added under that name
- **And** its folder is not renamed

### Scenario: Adding the chosen projects

- **Given** N rows are checked
- **When** I choose **Add N projects**
- **Then** those N projects appear on the Dashboard
- **And** I am told "N projects added"

### Scenario: Unchecked projects are remembered as ignored

- **Given** I left a project unchecked
- **When** the workspace is rescanned later
- **Then** that project is not presented again as new
