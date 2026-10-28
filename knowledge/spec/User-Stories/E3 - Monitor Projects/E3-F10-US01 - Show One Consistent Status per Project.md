---
id: E3-F10-US01
type: user-story
epic: Monitor Projects
feature: Project Status
status: Draft
personas:
  - Operator
source: "Server Control Panel – Product Spec & E2E Flow — §6 Status table, §7 Interface principles"
---
# E3-F10-US01 — Show One Consistent Status per Project

## User Story

As an Operator, I want every project to be in exactly one clearly named status, shown the same way everywhere, so that I never have to interpret what a colour means.

## Acceptance Criteria

### Scenario: The seven statuses

- **Given** any tracked project
- **When** its status is shown
- **Then** it is exactly one of these, with this colour and these actions:

| Status | When | Colour | Actions allowed |
|---|---|---|---|
| Running | All containers or processes are running | Green | Stop, Restart, Back up |
| Partial | Docker: only some containers are running | Yellow | Start, Stop, Restart |
| Stopped | Nothing is running, stopped by a person | Grey | Start, Back up (if the database still runs) |
| Error | A container exited with an error, pm2 reports `errored`, or it keeps restarting | Red | Start, Restart, View logs |
| Starting / Stopping | A command is running | Blue with spinner | None (locked) |
| Not found | Its folder disappeared after a rescan | Grey, struck through | Remove from list |
| Unknown | Its stack was not recognised | Pale yellow | Assign stack |

### Scenario: Status is never colour alone

- **Given** a status anywhere in the panel
- **When** it is shown
- **Then** it carries both a coloured dot and its name in words

### Scenario: The same status looks the same everywhere

- **Given** a project
- **When** I see it on a card, in a table and in its detail
- **Then** its status has the same name, colour and allowed actions in all three

### Scenario: Actions not allowed are not offered as if they work

- **Given** a project in a status that does not allow an action
- **When** I look at that action
- **Then** it is disabled, not hidden behind a control that appears to work
