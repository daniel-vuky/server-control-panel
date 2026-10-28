---
id: E4-F12-US03
type: user-story
epic: Control Projects
feature: Lifecycle Actions
status: Draft
personas:
  - Operator
source: "Server Control Panel – Product Spec & E2E Flow — §4.2 card menu, §4.3 Header"
---
# E4-F12-US03 — Restart a Project

## User Story

As an Operator, I want to restart a project in one action, so that I can recover a stuck or failing app quickly.

## Acceptance Criteria

### Scenario: Where restart is offered

- **Given** a project in Running, Partial or Error
- **When** I open its **⋯** menu or its detail header
- **Then** **Restart** is offered

### Scenario: Restarting

- **Given** I choose **Restart**
- **When** the command runs
- **Then** it is the restart command for its stack
- **And** the project is locked while it runs, following [[E4-F12-US04 - Lock a Project While a Command Runs]]

### Scenario: Restart succeeds

- **Given** a project is restarting
- **When** its components are running again
- **Then** its status becomes Running
- **And** I see a green confirmation naming the project

### Scenario: Restart fails

- **Given** a project is restarting
- **When** the command fails or takes longer than 60 seconds
- **Then** it is handled following [[E4-F12-US05 - Handle a Failed or Timed-Out Command]]
