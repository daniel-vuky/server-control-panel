---
id: E2-F05-US03
type: user-story
epic: Discover Projects
feature: Stack and Database Detection
status: Draft
personas:
  - Server Owner
source: "Server Control Panel – Product Spec & E2E Flow — §4.1 step 2, §4.3 Settings, Flow B step 5"
---
# E2-F05-US03 — Correct or Assign a Stack

## User Story

As the Server Owner, I want to change a project's stack when it was recognised wrongly or not at all, so that start, stop and metrics work for it.

## Acceptance Criteria

### Scenario: An unknown project stands out in scan results

- **Given** a scan result containing an `unknown` project
- **When** I review the results
- **Then** that row is highlighted in yellow
- **And** it offers a choice of stack in place of a stack badge

### Scenario: Correcting a stack before adding

- **Given** a project in scan results
- **When** I choose a different stack for it
- **Then** that stack is used when the project is added

### Scenario: Correcting a stack later

- **Given** a tracked project
- **When** I change its stack in its settings and save
- **Then** the project is started, stopped and measured as that stack from then on
- **And** the change is recorded with my email

### Scenario: An unknown project offers only stack assignment

- **Given** a tracked project whose stack is `unknown`
- **When** I look at it
- **Then** the only action offered is **Assign stack**, following [[E3-F10-US01 - Show One Consistent Status per Project]]
