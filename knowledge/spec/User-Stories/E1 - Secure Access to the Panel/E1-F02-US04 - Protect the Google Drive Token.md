---
id: E1-F02-US04
type: user-story
epic: Secure Access to the Panel
feature: Command Safety
status: Draft
personas:
  - Server Owner
source: "Server Control Panel – Product Spec & E2E Flow — §3 Security 6, Flow F step 1"
---
# E1-F02-US04 — Protect the Google Drive Token

## User Story

As the Server Owner, I want the panel's access to Google Drive to be as narrow as possible and stored safely, so that a leak of the panel's data does not expose my Drive.

## Acceptance Criteria

### Scenario: Only access to the panel's own files is requested

- **Given** I connect Google Drive
- **When** Google asks me to consent
- **Then** the only access requested is to files the panel creates
- **And** the panel cannot list or read anything else in my Drive

### Scenario: The token is stored encrypted

- **Given** Google Drive has been connected
- **When** the token is stored
- **Then** it is encrypted at rest
- **And** it is never shown in the interface or written to a log

### Scenario: Disconnecting removes the token

- **Given** Google Drive is connected
- **When** I disconnect it, following [[E5-F14-US03 - Reconnect or Disconnect Google Drive]]
- **Then** the stored token is deleted
