---
id: E7-F23-US04
type: user-story
epic: Track Activity and Alerts
feature: Alert Triggers
status: Draft
personas:
  - Server Owner
source: "Server Control Panel – Product Spec & E2E Flow — §6 External alerts, §4.2 Server summary bar"
---
# E7-F23-US04 — Alert When the Cloudflare Tunnel Disconnects

## User Story

As the Server Owner, I want to be alerted when the Cloudflare Tunnel drops, so that I know at once when every site on the server has become unreachable.

## Acceptance Criteria

### Scenario: The tunnel disconnects

- **Given** the Cloudflare Tunnel was connected
- **When** it loses its connection
- **Then** an alert is sent through every enabled channel

### Scenario: The alert must not depend on the tunnel

- **Given** the tunnel is down
- **When** the alert is sent
- **Then** it goes out directly from the server to Telegram or email, not through the tunnel

### Scenario: The tunnel reconnects

- **Given** the tunnel was down
- **When** it reconnects
- **Then** a recovery message is sent and the Dashboard shows it as connected
