---
id: E8-F25-US02
type: user-story
epic: Provide Consistent Interface Behavior
feature: Safe Interaction
status: Draft
personas:
  - Operator
source: "Server Control Panel – Product Spec & E2E Flow — §7 Interface principles"
---
# E8-F25-US02 — Keep Live Numbers Stable

## User Story

As an Operator, I want numbers that update live to stay in place as they change, so that I can read them without the layout jumping.

## Acceptance Criteria

### Scenario: Numbers use fixed-width digits

- **Given** any live value — CPU, RAM, disk, network, counts, durations
- **When** it is shown
- **Then** it uses monospaced or tabular digits

### Scenario: Updates do not shift the layout

- **Given** a value changes from, say, 9 % to 12 %
- **When** it updates
- **Then** nothing around it moves

### Scenario: Units are stated

- **Given** any number
- **When** it is shown
- **Then** its unit is shown with it — %, MB, GB, MB/s, s
