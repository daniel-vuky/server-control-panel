---
id: E2-F05-US01
type: user-story
epic: Discover Projects
feature: Project and Database Detection
status: Draft
personas:
  - Server Owner
source: "Server Control Panel – Product Spec & E2E Flow — §2 Stack detection rules 1–4"
---
# E2-F05-US01 — Detect Compose Projects

## User Story

As the Server Owner, I want each docker compose project recognised automatically, so that the panel knows how to start, stop and measure it without my telling it.

## Acceptance Criteria

### Scenario: A compose project

- **Given** a folder containing `compose.yaml`, `compose.yml`, `docker-compose.yml` or `docker-compose.yaml`
- **When** it is scanned
- **Then** it is listed as a project
- **And** its services, images, ports and volumes are read from the file

### Scenario: A folder without a compose file

- **Given** a folder that contains none of those files
- **When** it is scanned
- **Then** it is not listed

### Scenario: Current status is read during the scan

- **Given** a listed project
- **When** it is scanned
- **Then** its current status is taken from Docker
- **And** it is shown with the scan results
