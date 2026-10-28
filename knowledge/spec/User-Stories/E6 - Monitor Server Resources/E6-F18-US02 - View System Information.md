---
id: E6-F18-US02
type: user-story
epic: Monitor Server Resources
feature: Server Metrics
status: Draft
personas:
  - Operator
source: "Server Control Panel – Product Spec & E2E Flow — §4.5"
---
# E6-F18-US02 — View System Information

## User Story

As an Operator, I want the server's basic facts in one place, so that I can answer "what is this machine running" without logging in.

## Acceptance Criteria

### Scenario: What is shown

- **Given** I open Server
- **When** I look at system information
- **Then** I see the operating system, the kernel, the number of CPU cores and the total RAM
- **And** the versions of Docker, Node and pm2

### Scenario: Disks

- **Given** system information
- **When** I look at disks
- **Then** each disk is shown with its mount point, used and total space, and the percentage used against its alert threshold

### Scenario: A tool is missing

- **Given** Docker, Node or pm2 is not installed
- **When** its version is shown
- **Then** it says "Not installed" rather than leaving a blank
