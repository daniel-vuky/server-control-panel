---
id: E5-F14-US01
type: user-story
epic: Back Up Databases
feature: Google Drive Connection
status: Draft
personas:
  - Server Owner
source: "Server Control Panel – Product Spec & E2E Flow — §4.1 step 3, §4.7 Google Drive, Flow F steps 1–2"
---
# E5-F14-US01 — Connect Google Drive

## User Story

As the Server Owner, I want to connect a Google Drive account and pick a folder for backups, so that database backups leave the server and are kept somewhere safe.

## Acceptance Criteria

### Scenario: Starting the connection

- **Given** Google Drive is not connected
- **When** I choose **Connect Google Drive** in setup or in Settings
- **Then** Google's consent page opens
- **And** the only access asked for is to files the panel creates, following [[E1-F02-US04 - Protect the Google Drive Token]]

### Scenario: Returning connected

- **Given** I have consented on Google's page
- **When** I return to the panel
- **Then** I see the email of the connected account

### Scenario: Choosing the destination folder

- **Given** Google Drive is connected
- **When** I choose a destination folder, or create a new one
- **Then** backups are uploaded under it, in a sub-folder per project — `<folder>/<project>/`

### Scenario: Declining consent

- **Given** I am on Google's consent page
- **When** I decline or close it
- **Then** I return to the panel with Drive still not connected and told so

### Scenario: Leaving it for later

- **Given** I am in first-run setup
- **When** I choose **Later**
- **Then** setup continues without Drive
- **And** Backups shows a banner inviting me to connect, following [[E8-F24-US01 - Show Loading, Empty and Error States on Every Screen]]
