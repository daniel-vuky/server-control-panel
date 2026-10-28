---
id: E3-F11-US02
type: user-story
epic: Monitor Projects
feature: Project Detail
status: Draft
personas:
  - Operator
source: "Server Control Panel – Product Spec & E2E Flow — §4.3 Overview, Flow C step 3"
---
# E3-F11-US02 — Inspect a Project's Components

## User Story

As an Operator, I want to see each container or process inside a project, so that I can tell which part of it is running, failing or using resources.

## Acceptance Criteria

### Scenario: A Docker project's containers

- **Given** a `docker-compose` project
- **When** I look at its Overview
- **Then** each container is listed with its name, status, image, ports, CPU, RAM and restart count

### Scenario: A pm2 project's process

- **Given** a `nextjs-pm2` or `node-pm2` project
- **When** I look at its Overview
- **Then** I see its number of instances, its restart count and its PID

### Scenario: A failing component stands out

- **Given** a project in Partial or Error
- **When** I look at its components
- **Then** the components that are not running are marked with their status and exit reason

### Scenario: A summary line

- **Given** a project's components
- **When** I look at their heading
- **Then** I see how many of them are running out of how many
