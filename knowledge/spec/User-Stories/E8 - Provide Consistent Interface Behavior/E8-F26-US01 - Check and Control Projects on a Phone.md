---
id: E8-F26-US01
type: user-story
epic: Provide Consistent Interface Behavior
feature: Mobile Use
status: Draft
personas:
  - Operator
source: "Server Control Panel – Product Spec & E2E Flow — §1 Users, §7 Interface principles"
---
# E8-F26-US01 — Check and Control Projects on a Phone

## User Story

As an Operator, I want to check the server and start or stop a project from my phone, so that I can react when I am away from my desk.

## Acceptance Criteria

### Scenario: The Dashboard on a phone

- **Given** I open the panel on a phone
- **When** the Dashboard loads
- **Then** server measures are shown in a compact grid
- **And** project cards are stacked one per row
- **And** nothing scrolls sideways

### Scenario: Controls fit a finger

- **Given** a project card on a phone
- **When** I look at its Start or Stop control
- **Then** it is a full-width button at least 44 px tall, labelled with what it will do

### Scenario: Stopping on a phone

- **Given** I choose **Stop** on a phone
- **When** the confirmation appears
- **Then** it rises from the bottom of the screen with **Stop** and **Cancel** as large buttons

### Scenario: Charts and tables adapt

- **Given** a screen with charts or wide tables on a phone
- **When** it is shown
- **Then** charts are reduced to their essential lines
- **And** wide tables scroll inside their own box

### Scenario: Navigation

- **Given** a phone
- **When** I want another screen
- **Then** a menu button opens the same navigation as the desktop sidebar
