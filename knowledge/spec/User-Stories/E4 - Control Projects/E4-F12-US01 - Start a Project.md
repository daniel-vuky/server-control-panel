---
id: E4-F12-US01
type: user-story
epic: Control Projects
feature: Lifecycle Actions
status: Draft
personas:
  - Operator
source: "Server Control Panel – Product Spec & E2E Flow — Flow D, §6 Status table"
---
# E4-F12-US01 — Start a Project

## User Story

As an Operator, I want to start a project with one action, so that I can bring a site back without opening a terminal.

## Acceptance Criteria

### Scenario: Starting runs without a confirmation

- **Given** a project in Stopped, Partial or Error
- **When** I choose **Start** on its card, its table row or its detail
- **Then** it starts at once, with no confirmation asked

### Scenario: The command is a compose command

- **Given** I start a project
- **When** the command runs
- **Then** it is `docker compose up -d`, or the template chosen in its settings

### Scenario: Starting succeeds

- **Given** a project is starting
- **When** the command finishes and its components are running
- **Then** its status becomes Running everywhere it is shown
- **And** I see a green confirmation naming the project

### Scenario: Starting fails

- **Given** a project is starting
- **When** the command fails or takes longer than 60 seconds
- **Then** it is handled following [[E4-F12-US05 - Handle a Failed or Timed-Out Command]]

### Scenario: The action is recorded

- **Given** a start, successful or not
- **When** it ends
- **Then** it is recorded following [[E1-F03-US01 - Attribute Every Action to a Person]]
