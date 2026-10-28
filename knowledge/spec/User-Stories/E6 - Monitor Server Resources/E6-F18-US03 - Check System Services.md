---
id: E6-F18-US03
type: user-story
epic: Monitor Server Resources
feature: Server Metrics
status: Draft
personas:
  - Operator
source: "Server Control Panel – Product Spec & E2E Flow — §4.5"
---
# E6-F18-US03 — Check System Services

## User Story

As an Operator, I want to see whether Docker and cloudflared are healthy, so that I can tell a problem with one project from a problem with the server.

## Acceptance Criteria

### Scenario: The two services

- **Given** I open Server
- **When** I look at system services
- **Then** I see the Docker daemon and cloudflared, each with its state in words and colour

### Scenario: What each adds

- **Given** a service that is healthy
- **When** I look at it
- **Then** Docker shows how many containers it runs, and cloudflared how many hostnames it serves

### Scenario: A service is down

- **Given** a service is not responding
- **When** I look at it
- **Then** it is shown in red as not responding
- **And** the effect elsewhere follows [[E4-F13-US01 - Lock Controls When Docker Is Unresponsive]] or [[E7-F23-US04 - Alert When the Cloudflare Tunnel Disconnects]]
