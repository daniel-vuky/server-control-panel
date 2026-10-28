---
id: E3-F11-US05
type: user-story
epic: Monitor Projects
feature: Project Detail
status: Draft
personas:
  - Server Owner
source: "Server Control Panel – Product Spec & E2E Flow — §4.3 Settings"
---
# E3-F11-US05 — Edit Project Settings

## User Story

As the Server Owner, I want to adjust how a project is named, run and watched, so that the panel matches how the project really works.

## Acceptance Criteria

### Scenario: What can be changed

- **Given** a project's Settings tab
- **When** I look at it
- **Then** I can change its display name, its stack, its public domain, its start/stop command template and its own alert thresholds

### Scenario: Start and stop commands come from templates

- **Given** I change the start/stop command
- **When** I open the choice
- **Then** I pick from templates for its stack, following [[E1-F02-US01 - Run Only Whitelisted Commands]]

### Scenario: Saving

- **Given** I have changed one or more settings
- **When** I choose **Save changes**
- **Then** they take effect at once
- **And** the change is recorded with my email

### Scenario: Leaving without saving

- **Given** I have unsaved changes
- **When** I leave the tab
- **Then** I am asked whether to discard them

### Scenario: Stopping tracking

- **Given** the Settings tab
- **When** I look at its end
- **Then** **Stop tracking** is set apart in red, following [[E2-F06-US03 - Stop Tracking a Project]]
