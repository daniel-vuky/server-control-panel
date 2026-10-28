---
id: E3-F09-US01
type: user-story
epic: Monitor Projects
feature: Live Updates
status: Draft
personas:
  - Operator
source: "Server Control Panel – Product Spec & E2E Flow — Flow C step 1"
---
# E3-F09-US01 — Receive Live Status and Metrics

## User Story

As an Operator, I want status and metrics to update on their own, so that what I see is current without my reloading.

## Acceptance Criteria

### Scenario: Status updates every five seconds

- **Given** I have the Dashboard or a project's detail open
- **When** a project's status changes on the server
- **Then** the change appears within five seconds

### Scenario: Metrics update every ten seconds

- **Given** I have the Dashboard, a project's detail or Server open
- **When** CPU, RAM, disk or network values change
- **Then** the new values appear within ten seconds

### Scenario: Updates are pushed, not reloaded

- **Given** any screen with live values
- **When** values update
- **Then** only the values change
- **And** my scroll position, filters, open menus and anything I am typing stay as they were
