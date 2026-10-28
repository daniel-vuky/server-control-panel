---
id: E4-F12-US04
type: user-story
epic: Control Projects
feature: Lifecycle Actions
status: Draft
personas:
  - Operator
source: "Server Control Panel – Product Spec & E2E Flow — Flow D step 3, §6 Starting/Stopping"
---
# E4-F12-US04 — Lock a Project While a Command Runs

## User Story

As an Operator, I want a project to show that a command is running and refuse other actions until it ends, so that two commands never fight over the same project.

## Acceptance Criteria

### Scenario: The in-between status

- **Given** I start, stop or restart a project
- **When** the command is running
- **Then** the project shows Starting… or Stopping… in blue with a spinner, on its card, its row and its detail

### Scenario: Other actions are locked

- **Given** a project in Starting or Stopping
- **When** I look at its actions
- **Then** all of them are disabled, including those in its **⋯** menu and its detail

### Scenario: The lock holds for everyone

- **Given** a project in Starting or Stopping because of my action
- **When** another Operator looks at it
- **Then** they see the same status and the same locked actions

### Scenario: Other projects stay free

- **Given** one project is locked
- **When** I act on a different project
- **Then** that action runs normally

### Scenario: The lock ends with the command

- **Given** a project is locked
- **When** its command succeeds, fails or times out
- **Then** the lock is released and the project shows its resulting status
