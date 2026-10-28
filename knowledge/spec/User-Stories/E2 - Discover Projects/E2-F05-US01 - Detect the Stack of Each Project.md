---
id: E2-F05-US01
type: user-story
epic: Discover Projects
feature: Stack and Database Detection
status: Draft
personas:
  - Server Owner
source: "Server Control Panel – Product Spec & E2E Flow — §2 Stack detection rules 1–4"
---
# E2-F05-US01 — Detect the Stack of Each Project

## User Story

As the Server Owner, I want each project's stack recognised automatically, so that the panel knows how to start, stop and measure it without my telling it.

## Acceptance Criteria

### Scenario: A compose project

- **Given** a folder containing `docker-compose.yml` or `compose.yaml`
- **When** it is scanned
- **Then** its stack is `docker-compose`
- **And** its services, images, ports and volumes are read from the file

### Scenario: A Next.js project run by pm2

- **Given** a folder whose `package.json` depends on `next`
- **And** its app name appears in `pm2 jlist`, or it has an `ecosystem.config.js`
- **When** it is scanned
- **Then** its stack is `nextjs-pm2`

### Scenario: A Next.js project run by node

- **Given** a folder whose `package.json` depends on `next`
- **And** neither pm2 nor an `ecosystem.config.js` knows it
- **When** it is scanned
- **Then** its stack is `nextjs-node`

### Scenario: A Node project run by pm2

- **Given** a folder whose `package.json` does not depend on `next`
- **And** its app name appears in `pm2 jlist`
- **When** it is scanned
- **Then** its stack is `node-pm2`

### Scenario: Nothing matches

- **Given** a folder that matches none of the rules
- **When** it is scanned
- **Then** its stack is `unknown`
- **And** it is still listed, so I can assign a stack or ignore it, following [[E2-F05-US03 - Correct or Assign a Stack]]

### Scenario: Current status is read during the scan

- **Given** a recognised project
- **When** it is scanned
- **Then** its current status is taken from `docker ps` or `pm2 jlist`
- **And** it is shown with the scan results
