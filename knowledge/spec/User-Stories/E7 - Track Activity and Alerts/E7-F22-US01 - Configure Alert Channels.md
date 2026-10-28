---
id: E7-F22-US01
type: user-story
epic: Track Activity and Alerts
feature: Alert Configuration
status: Draft
personas:
  - Server Owner
source: "Server Control Panel – Product Spec & E2E Flow — §4.7 Alerts"
---
# E7-F22-US01 — Configure Alert Channels

## User Story

As the Server Owner, I want to choose where alerts are sent — Telegram, email or both — so that problems reach me where I will see them.

## Acceptance Criteria

### Scenario: Setting up Telegram

- **Given** Settings
- **When** I enter a Telegram bot token and a chat ID and save
- **Then** Telegram is added as a channel
- **And** the token is stored masked and never shown whole again

### Scenario: Setting up email

- **Given** Settings
- **When** I enter one or more email addresses and save
- **Then** email is added as a channel

### Scenario: Turning a channel on or off

- **Given** a configured channel
- **When** I switch it off
- **Then** no alert is sent through it until I switch it back on
- **And** alerts are still recorded in Activity

### Scenario: Sending a test

- **Given** a configured channel
- **When** I choose **Send test**
- **Then** a test message is sent through it
- **And** I am told whether it was delivered or why it failed
