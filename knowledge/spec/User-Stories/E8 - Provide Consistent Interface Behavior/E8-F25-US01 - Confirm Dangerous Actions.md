---
id: E8-F25-US01
type: user-story
epic: Provide Consistent Interface Behavior
feature: Safe Interaction
status: Draft
personas:
  - Operator
source: "Server Control Panel – Product Spec & E2E Flow — §7 Interface principles, Flow D step 2"
---
# E8-F25-US01 — Confirm Dangerous Actions

## User Story

As an Operator, I want every action that takes something away to look different and ask before it runs, so that nothing destructive happens from a single slip.

## Acceptance Criteria

### Scenario: Which actions count

- **Given** the actions Stop, Remove workspace, Stop tracking, Disconnect Google Drive and Remove from list
- **When** any of them is offered
- **Then** it is styled in red

### Scenario: They always ask

- **Given** one of these actions
- **When** I choose it
- **Then** a confirmation says what will be affected and what will not
- **And** its confirming button repeats the action's name in red, never a bare "OK"

### Scenario: Cancelling is safe and obvious

- **Given** a confirmation is open
- **When** I choose **Cancel**, press Escape or click outside it
- **Then** nothing happens

### Scenario: Non-destructive actions do not ask

- **Given** Start, Restart from Error, or Back up now
- **When** I choose it
- **Then** it runs without a confirmation
