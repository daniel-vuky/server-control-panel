---
id: E4-F12-US05
type: user-story
epic: Control Projects
feature: Lifecycle Actions
status: Draft
personas:
  - Operator
source: "Server Control Panel – Product Spec & E2E Flow — Flow D step 5"
---
# E4-F12-US05 — Handle a Failed or Timed-Out Command

## User Story

As an Operator, I want a failed start, stop or restart to tell me clearly what happened and where to look, so that I am never left guessing about the state of a project.

## Acceptance Criteria

### Scenario: The command fails

- **Given** a start, stop or restart is running
- **When** the command returns an error
- **Then** the project returns to the status it had before
- **And** I see a red message naming the project and the error, with a link to **View logs**

### Scenario: The command takes too long

- **Given** a start, stop or restart is running
- **When** 60 seconds pass without it finishing
- **Then** it is treated as failed and handled in the same way
- **And** the message says it timed out

### Scenario: The status is re-read, not assumed

- **Given** a command has failed or timed out
- **When** its status is shown afterwards
- **Then** it is the status actually read from Docker or pm2, not the one I asked for

### Scenario: The failure is recorded

- **Given** a failed or timed-out command
- **When** it ends
- **Then** it is recorded with its error, following [[E1-F03-US01 - Attribute Every Action to a Person]]
