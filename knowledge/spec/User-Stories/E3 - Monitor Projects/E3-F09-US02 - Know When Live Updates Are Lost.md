---
id: E3-F09-US02
type: user-story
epic: Monitor Projects
feature: Live Updates
status: Draft
personas:
  - Operator
source: "Server Control Panel – Product Spec & E2E Flow — §6 Common errors"
---
# E3-F09-US02 — Know When Live Updates Are Lost

## User Story

As an Operator, I want to be told when live updates stop, so that I never act on numbers that are out of date.

## Acceptance Criteria

### Scenario: The connection drops

- **Given** live updates are flowing
- **When** the connection to the panel is lost
- **Then** a badge at the top says "Connection lost, retrying…"
- **And** the values on screen are dimmed to show they may be out of date

### Scenario: The connection comes back

- **Given** the connection was lost
- **When** it is restored
- **Then** the badge goes away
- **And** values are refreshed and shown at full strength

### Scenario: Retrying happens on its own

- **Given** the connection is lost
- **When** I do nothing
- **Then** the panel keeps trying to reconnect
