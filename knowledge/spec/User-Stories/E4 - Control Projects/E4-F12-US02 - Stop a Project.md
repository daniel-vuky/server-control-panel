---
id: E4-F12-US02
type: user-story
epic: Control Projects
feature: Lifecycle Actions
status: Draft
personas:
  - Operator
source: "Server Control Panel – Product Spec & E2E Flow — Flow D steps 1–6"
---
# E4-F12-US02 — Stop a Project

## User Story

As an Operator, I want to stop a project only after confirming what that will take offline, so that I never take a live site down by a slip.

## Acceptance Criteria

### Scenario: Stopping asks first

- **Given** a project in Running or Partial
- **When** I choose **Stop** on its card, its table row or its detail
- **Then** a confirmation asks "Stop <name>? Domain <domain> will stop serving."
- **And** it shows the command that will run
- **And** its confirming button is red and says **Stop**

### Scenario: Cancelling

- **Given** the stop confirmation is open
- **When** I choose **Cancel** or close it
- **Then** nothing is run and the project is as it was

### Scenario: Confirming

- **Given** the stop confirmation is open
- **When** I choose **Stop**
- **Then** the project goes into Stopping, following [[E4-F12-US04 - Lock a Project While a Command Runs]]
- **And** the stop command for its stack runs — `docker compose stop` or `pm2 stop <app>` — with a 60-second limit

### Scenario: Stopping succeeds

- **Given** a project is stopping
- **When** the command finishes and nothing of it is running
- **Then** its status becomes Stopped everywhere it is shown
- **And** I see a green confirmation naming the project

### Scenario: Stopping fails

- **Given** a project is stopping
- **When** the command fails or takes longer than 60 seconds
- **Then** it is handled following [[E4-F12-US05 - Handle a Failed or Timed-Out Command]]

### Scenario: The action is recorded

- **Given** a stop, successful or not
- **When** it ends
- **Then** it is recorded following [[E1-F03-US01 - Attribute Every Action to a Person]]
