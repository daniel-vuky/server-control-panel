---
id: E7-F23-US01
type: user-story
epic: Track Activity and Alerts
feature: Alert Triggers
status: Draft
personas:
  - Operator
source: "Server Control Panel – Product Spec & E2E Flow — §6 External alerts"
---
# E7-F23-US01 — Alert When a Project Fails or Its Domain Stops Responding

## User Story

As an Operator, I want to be alerted when a project goes into Error or its site stops answering, so that I can react before users complain.

## Acceptance Criteria

### Scenario: A project goes into Error

- **Given** a project that was not in Error
- **When** its status becomes Error
- **Then** an alert is sent through every enabled channel, naming the project and what failed

### Scenario: A domain stops responding

- **Given** a project with a public domain, checked every 60 seconds
- **When** the domain fails to respond on 3 consecutive checks
- **Then** an alert is sent naming the project and the domain

### Scenario: One alert per incident

- **Given** an alert was sent for a project
- **When** the problem continues
- **Then** no further alert is sent for it until it has recovered and failed again

### Scenario: Recovery

- **Given** a project that raised an alert
- **When** it is Running and its domain answers again
- **Then** a recovery message is sent

### Scenario: Stopped by a person is not a failure

- **Given** a project stopped through the panel
- **When** its domain stops responding
- **Then** no alert is sent
