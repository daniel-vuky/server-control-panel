---
id: E6-F19-US01
type: user-story
epic: Monitor Server Resources
feature: Usage by App
status: Draft
personas:
  - Operator
source: "Server Control Panel – Product Spec & E2E Flow — §4.5 Usage ranking, Flow C step 4"
---
# E6-F19-US01 — Rank Apps by Resource Usage

## User Story

As an Operator, I want projects ranked by how much of the server they use, so that I can find the app eating the most resources.

## Acceptance Criteria

### Scenario: The ranking table

- **Given** I open Server
- **When** I look at usage by app
- **Then** each project is a row with its CPU %, RAM, disk — volumes and folder together — and network

### Scenario: Share of the whole

- **Given** a value in the table
- **When** I look at it
- **Then** a horizontal bar and a percentage show its share of the server's total for that measure

### Scenario: Sorting

- **Given** the table
- **When** I choose a column heading
- **Then** projects are sorted by that measure, largest first
- **And** the heading shows that it is the sort

### Scenario: Opening a project

- **Given** a row
- **When** I choose the project's name
- **Then** its detail opens
