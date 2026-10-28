---
id: E7-F21-US01
type: user-story
epic: Track Activity and Alerts
feature: Activity Log
status: Draft
personas:
  - Operator
source: "Server Control Panel – Product Spec & E2E Flow — §4.6, §3 Security 7"
---
# E7-F21-US01 — Review the Activity Timeline

## User Story

As an Operator, I want a timeline of everything done in the panel and every alert raised, so that I can reconstruct what happened to a project and who did it.

## Acceptance Criteria

### Scenario: The timeline

- **Given** I open Activity
- **When** I look at it
- **Then** entries are grouped by day, newest first
- **And** each shows the time, who acted — a person's email, the schedule or the system — what was done, on which project, the details and the result

### Scenario: Alerts are part of it

- **Given** an alert was raised — an app crashed, a backup failed, the disk filled
- **When** I look at the timeline
- **Then** the alert is an entry, marked as an alert, with the channels it was sent to

### Scenario: Filtering

- **Given** the timeline
- **When** I filter by kind — actions, backups or alerts — or by project
- **Then** only matching entries are shown

### Scenario: Going to the project

- **Given** an entry about a project
- **When** I choose the project's name
- **Then** its detail opens

### Scenario: Entries are kept

- **Given** an entry
- **When** I come back later
- **Then** it is still there and unchanged, following [[E1-F03-US01 - Attribute Every Action to a Person]]
