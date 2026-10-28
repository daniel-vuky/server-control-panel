---
id: E3-F11-US01
type: user-story
epic: Monitor Projects
feature: Project Detail
status: Draft
personas:
  - Operator
source: "Server Control Panel – Product Spec & E2E Flow — §4.3 Header, Flow C step 2"
---
# E3-F11-US01 — Open a Project's Detail

## User Story

As an Operator, I want a page for each project with its identity and its main actions at the top, so that I can understand and act on one project in one place.

## Acceptance Criteria

### Scenario: The header

- **Given** I open a project's detail
- **When** I look at its header
- **Then** I see its name, stack, status, path on the server, public domain and uptime
- **And** **Start**, **Stop**, **Restart** and **Back up now**, each enabled only when its status allows it

### Scenario: Four tabs

- **Given** a project's detail
- **When** I look below the header
- **Then** I see the tabs Overview, Logs, Database & Backup and Settings
- **And** Overview is open first

### Scenario: Coming from a card in error

- **Given** a project in Error on the Dashboard
- **When** I open it
- **Then** Overview shows which component failed and its restart count
- **And** Logs is one step away, showing the most recent error

### Scenario: Going back

- **Given** a project's detail
- **When** I choose Projects in its breadcrumb
- **Then** I return to the Dashboard with my filters kept
