---
id: E2-F04-US02
type: user-story
epic: Discover Projects
feature: Workspaces
status: Draft
personas:
  - Server Owner
source: "Server Control Panel – Product Spec & E2E Flow — Flow B step 3, §6 Empty states"
---
# E2-F04-US02 — Follow Scan Progress

## User Story

As the Server Owner, I want to see how far a scan has got, so that I know it is working and roughly how long is left.

## Acceptance Criteria

### Scenario: Progress is shown while scanning

- **Given** a scan is running
- **When** I watch it
- **Then** I see how many folders have been examined out of how many, such as "Scanning… 12/30 folders"
- **And** the count moves as the scan advances

### Scenario: The scan finishes

- **Given** a scan is running
- **When** it finishes
- **Then** I am taken to its results, following [[E2-F06-US01 - Choose Which Scanned Projects to Track]]

### Scenario: The scan finds nothing

- **Given** a scan finishes without finding any project
- **When** its result is shown
- **Then** I am told no project was found
- **And** I am offered to scan again with a greater depth or to check the path
