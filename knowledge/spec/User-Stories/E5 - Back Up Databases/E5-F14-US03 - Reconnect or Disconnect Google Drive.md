---
id: E5-F14-US03
type: user-story
epic: Back Up Databases
feature: Google Drive Connection
status: Draft
personas:
  - Server Owner
source: "Server Control Panel – Product Spec & E2E Flow — §4.7 Google Drive, §6 Common errors"
---
# E5-F14-US03 — Reconnect or Disconnect Google Drive

## User Story

As the Server Owner, I want to reconnect Drive when its access expires and disconnect it when I no longer want it, so that backups never fail silently and access is never left open by mistake.

## Acceptance Criteria

### Scenario: The token has expired

- **Given** the Google Drive token has expired or been revoked
- **When** I open any screen
- **Then** a yellow banner says to reconnect Google Drive
- **And** it offers **Reconnect Google Drive**

### Scenario: Reconnecting

- **Given** the token has expired
- **When** I reconnect and consent again
- **Then** the banner goes away
- **And** the destination folder I chose before is kept

### Scenario: Disconnecting asks first

- **Given** Google Drive is connected
- **When** I choose **Disconnect**
- **Then** I am told scheduled backups will fail until Drive is connected again
- **And** the confirming button is red

### Scenario: Disconnecting

- **Given** I confirmed disconnecting
- **When** it is done
- **Then** the stored token is deleted
- **And** backups already on Drive are left as they are
