---
id: E6-F20-US01
type: user-story
epic: Monitor Server Resources
feature: Metrics History
status: Draft
personas:
  - Server Owner
source: "Server Control Panel – Product Spec & E2E Flow — §2 History, §3 Scheduler"
---
# E6-F20-US01 — Sample and Retain Metrics

## User Story

As the Server Owner, I want metrics sampled regularly and kept for a set time, so that charts have history without the panel filling the disk.

## Acceptance Criteria

### Scenario: Sampling

- **Given** the panel is running
- **When** time passes
- **Then** server and per-project metrics are sampled every 30 to 60 seconds and stored

### Scenario: Retention

- **Given** stored metrics
- **When** they become older than the retention period, between 7 and 30 days
- **Then** they are deleted

### Scenario: Sampling continues without anyone watching

- **Given** nobody has the panel open
- **When** time passes
- **Then** sampling continues, so history has no gaps caused by closed browsers

### Scenario: The panel's own usage is visible

- **Given** sampling is running
- **When** I look at usage by app
- **Then** the panel itself is listed as a row, so its own cost on the server can be seen
