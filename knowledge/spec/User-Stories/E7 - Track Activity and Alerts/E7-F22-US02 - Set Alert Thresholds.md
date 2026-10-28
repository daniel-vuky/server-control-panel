---
id: E7-F22-US02
type: user-story
epic: Track Activity and Alerts
feature: Alert Configuration
status: Draft
personas:
  - Server Owner
source: "Server Control Panel – Product Spec & E2E Flow — §4.7 Alerts, §6 External alerts"
---
# E7-F22-US02 — Set Alert Thresholds

## User Story

As the Server Owner, I want to set the levels at which CPU, RAM and disk raise an alert, so that I am warned at the point that matters for my server.

## Acceptance Criteria

### Scenario: Default thresholds

- **Given** thresholds have never been changed
- **When** I open them
- **Then** CPU is 90 %, RAM 90 %, disk 85 %, and the time a value must stay over is 5 minutes

### Scenario: Changing them

- **Given** the thresholds
- **When** I change any of them and save
- **Then** alerts, the Dashboard colours and the lines on Server charts all use the new values

### Scenario: Out-of-range values

- **Given** I enter a percentage below 1 or above 100, or a duration below 1 minute
- **When** I try to save
- **Then** I am told which value is out of range and nothing is saved

### Scenario: A project's own thresholds

- **Given** a project with its own thresholds set in its settings
- **When** its usage is checked
- **Then** its own thresholds are used for it in place of the server-wide ones
