---
id: E7-F23-US03
type: user-story
epic: Track Activity and Alerts
feature: Alert Triggers
status: Draft
personas:
  - Server Owner
source: "Server Control Panel – Product Spec & E2E Flow — §6 External alerts"
---
# E7-F23-US03 — Alert When Resources Stay Over Threshold

## User Story

As the Server Owner, I want to be alerted when CPU, RAM or disk stays over its threshold, so that I can act before the server slows down or fills up.

## Acceptance Criteria

### Scenario: Staying over the threshold

- **Given** CPU, RAM or disk goes over its threshold
- **When** it stays over for the set duration, 5 minutes by default
- **Then** an alert is sent naming the measure, its value and its threshold

### Scenario: A short spike

- **Given** a measure goes over its threshold
- **When** it drops back under before the set duration
- **Then** no alert is sent

### Scenario: One alert per breach

- **Given** an alert was sent for a measure
- **When** it stays over
- **Then** no further alert is sent until it has gone back under and over again
