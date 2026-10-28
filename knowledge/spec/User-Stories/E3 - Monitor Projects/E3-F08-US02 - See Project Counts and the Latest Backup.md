---
id: E3-F08-US02
type: user-story
epic: Monitor Projects
feature: Dashboard
status: Draft
personas:
  - Operator
source: "Server Control Panel – Product Spec & E2E Flow — §4.2 Quick counts"
---
# E3-F08-US02 — See Project Counts and the Latest Backup

## User Story

As an Operator, I want counts of projects by status and the latest backup in one line, so that I know whether anything needs me before reading the cards.

## Acceptance Criteria

### Scenario: Counts by status

- **Given** the tracked projects
- **When** I look at the Dashboard
- **Then** I see how many are running, partial, stopped and in error
- **And** each count carries its status colour and its word

### Scenario: The latest backup

- **Given** at least one backup has run
- **When** I look at the same line
- **Then** I see which project was backed up last, when, and whether it succeeded
- **And** a link takes me to Backups

### Scenario: No backup yet

- **Given** no backup has run
- **When** I look at the same line
- **Then** I am told there is no backup yet

### Scenario: Counts follow live changes

- **Given** a project's status changes
- **When** the Dashboard updates
- **Then** the counts change with it, without my reloading the page
