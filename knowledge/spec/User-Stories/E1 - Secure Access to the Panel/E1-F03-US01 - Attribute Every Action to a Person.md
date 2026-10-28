---
id: E1-F03-US01
type: user-story
epic: Secure Access to the Panel
feature: Accountability
status: Draft
personas:
  - Server Owner
source: "Server Control Panel – Product Spec & E2E Flow — §3 Security 7, Flow D step 6"
---
# E1-F03-US01 — Attribute Every Action to a Person

## User Story

As the Server Owner, I want every action taken in the panel recorded with the email of the person who took it, so that I can always tell who did what and when.

## Acceptance Criteria

### Scenario: A person's action is recorded with their email

- **Given** I start, stop or restart a project, run a backup, scan a workspace or change a setting
- **When** the action is taken
- **Then** an entry is recorded with my email, what I did, on which project, when, and the result
- **And** the email is the one Cloudflare Access identified, not anything the browser supplied

### Scenario: Scheduled actions are recorded as the schedule

- **Given** a backup runs on schedule
- **When** it is recorded
- **Then** it is attributed to the schedule, not to a person

### Scenario: A failed or refused action is still recorded

- **Given** an action fails, times out or is refused
- **When** it ends
- **Then** it is recorded with its result and the reason

### Scenario: Records cannot be edited from the panel

- **Given** a recorded action
- **When** I view it in [[E7-F21-US01 - Review the Activity Timeline]]
- **Then** nothing about it can be changed or deleted from the interface
