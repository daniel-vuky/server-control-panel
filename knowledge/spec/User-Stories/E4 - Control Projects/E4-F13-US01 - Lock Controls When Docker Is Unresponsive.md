---
id: E4-F13-US01
type: user-story
epic: Control Projects
feature: Runtime Availability
status: Draft
personas:
  - Operator
source: "Server Control Panel – Product Spec & E2E Flow — §6 Common errors, §4.5 System services"
---
# E4-F13-US01 — Lock Controls When Docker Is Unresponsive

## User Story

As an Operator, I want to be told plainly when Docker is not responding, so that I do not press buttons that cannot work.

## Acceptance Criteria

### Scenario: Docker stops responding

- **Given** the Docker daemon does not respond
- **When** I open any screen
- **Then** a red banner across the page says Docker is not responding
- **And** the start, stop, restart and backup actions of every project are disabled

### Scenario: Trying again

- **Given** the banner is shown
- **When** I choose **Retry**, or the service comes back on its own
- **Then** the panel checks again
- **And** if the service answers, the banner goes away and the actions are enabled
