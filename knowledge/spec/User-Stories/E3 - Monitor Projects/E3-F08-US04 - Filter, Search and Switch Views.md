---
id: E3-F08-US04
type: user-story
epic: Monitor Projects
feature: Dashboard
status: Draft
personas:
  - Operator
source: "Server Control Panel – Product Spec & E2E Flow — §4.2 Filters"
---
# E3-F08-US04 — Filter, Search and Switch Views

## User Story

As an Operator, I want to narrow the projects shown and choose between cards and a table, so that I can find a project quickly as their number grows.

## Acceptance Criteria

### Scenario: Filtering by status

- **Given** the Dashboard
- **When** I choose a status filter — All, Running, Partial, Stopped or Error
- **Then** only projects with that status are shown
- **And** each filter shows how many projects it holds

### Scenario: Searching by name

- **Given** the Dashboard
- **When** I type in the search box
- **Then** only projects whose name contains what I typed are shown

### Scenario: Filters combine

- **Given** a status and a search are both set
- **When** the list is shown
- **Then** only projects matching both appear

### Scenario: Switching between cards and a table

- **Given** the Dashboard
- **When** I switch to **Table**
- **Then** the same projects are shown as rows with the same information and the same Start/Stop control
- **And** switching back to **Cards** keeps my filters

### Scenario: Nothing matches

- **Given** filters that match no project
- **When** the list is shown
- **Then** I am told nothing matches and offered to clear the filters
