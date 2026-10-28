---
id: E5-F14-US02
type: user-story
epic: Back Up Databases
feature: Google Drive Connection
status: Draft
personas:
  - Server Owner
source: "Server Control Panel – Product Spec & E2E Flow — §4.7 Google Drive, Flow F step 2"
---
# E5-F14-US02 — Test the Drive Connection

## User Story

As the Server Owner, I want to check that the panel can really write to my Drive folder, so that I find out about a problem before a backup fails.

## Acceptance Criteria

### Scenario: A successful test

- **Given** Google Drive is connected and a destination folder is chosen
- **When** I choose **Test connection**
- **Then** a small file is uploaded to the folder and removed again
- **And** I am told the test succeeded

### Scenario: A failed test

- **Given** Google Drive is connected
- **When** the test cannot upload or remove the file
- **Then** I am told it failed and why — no access, folder missing, token expired or Drive unreachable

### Scenario: The test is in progress

- **Given** I started a test
- **When** it is running
- **Then** the button shows it is working and cannot be pressed again
