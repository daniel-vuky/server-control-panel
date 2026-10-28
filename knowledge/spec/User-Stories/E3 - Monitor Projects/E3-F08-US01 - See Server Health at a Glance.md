---
id: E3-F08-US01
type: user-story
epic: Monitor Projects
feature: Dashboard
status: Draft
personas:
  - Operator
source: "Server Control Panel – Product Spec & E2E Flow — §4.2 Server summary bar"
---
# E3-F08-US01 — See Server Health at a Glance

## User Story

As an Operator, I want the server's key numbers at the top of the Dashboard, so that I can tell in a second whether the machine itself is healthy.

## Acceptance Criteria

### Scenario: The summary shows six measures

- **Given** I am on the Dashboard
- **When** I look at the summary bar
- **Then** I see CPU %, RAM used of total, disk used of total, load average, uptime and the Cloudflare Tunnel status

### Scenario: Each measure shows its last hour

- **Given** the summary bar
- **When** I look at a measure
- **Then** it carries a small chart of its last hour next to its current value

### Scenario: A measure over its threshold stands out

- **Given** CPU, RAM or disk is above its alert threshold, following [[E7-F22-US02 - Set Alert Thresholds]]
- **When** I look at the summary bar
- **Then** that measure changes colour and is labelled as over threshold
- **And** the others keep their normal look

### Scenario: The tunnel is down

- **Given** the Cloudflare Tunnel has lost its connection
- **When** I look at the summary bar
- **Then** the tunnel measure says it is disconnected, in words and in colour
