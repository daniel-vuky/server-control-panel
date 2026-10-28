---
id: E6-F18-US01
type: user-story
epic: Monitor Server Resources
feature: Server Metrics
status: Draft
personas:
  - Operator
source: "Server Control Panel – Product Spec & E2E Flow — §4.5"
---
# E6-F18-US01 — Chart Server Resources Over Time

## User Story

As an Operator, I want large charts of the server's CPU, RAM, disk I/O and network over time, so that I can see trends and spikes for the whole machine.

## Acceptance Criteria

### Scenario: Four charts

- **Given** I open Server
- **When** I look at its charts
- **Then** I see CPU, RAM, disk I/O with read and write, and network with in and out
- **And** each shows its current value and its scale

### Scenario: Choosing a period

- **Given** the charts
- **When** I choose 1 hour, 24 hours or 7 days
- **Then** all four show that period

### Scenario: Thresholds are drawn

- **Given** a chart of CPU or RAM
- **When** it is drawn
- **Then** its alert threshold is shown as a line on it

### Scenario: Two series are told apart

- **Given** a chart with two series, such as read and write
- **When** it is drawn
- **Then** the series differ in line style as well as colour, and a legend names them
