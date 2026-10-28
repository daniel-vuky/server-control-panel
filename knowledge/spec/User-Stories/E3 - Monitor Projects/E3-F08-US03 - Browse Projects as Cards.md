---
id: E3-F08-US03
type: user-story
epic: Monitor Projects
feature: Dashboard
status: Draft
personas:
  - Operator
source: "Server Control Panel – Product Spec & E2E Flow — §4.2 Project card grid"
---
# E3-F08-US03 — Browse Projects as Cards

## User Story

As an Operator, I want each project shown as a card with its essentials, so that I can see the state of every project without opening any of them.

## Acceptance Criteria

### Scenario: What a card shows

- **Given** a tracked project
- **When** I look at its card
- **Then** I see its name, its status, its public domain, its CPU % and RAM, its uptime and its last backup

### Scenario: Opening the domain

- **Given** a card
- **When** I choose its domain
- **Then** the site opens in a new tab

### Scenario: Starting or stopping from the card

- **Given** a card
- **When** I use its Start/Stop toggle
- **Then** the project starts or stops following [[E4-F12-US01 - Start a Project]] and [[E4-F12-US02 - Stop a Project]]

### Scenario: More actions from the card

- **Given** a card
- **When** I open its **⋯** menu
- **Then** I am offered **Restart**, **Back up now**, **View logs** and **Open details**
- **And** only the actions allowed for its status are enabled

### Scenario: Opening the project

- **Given** a card
- **When** I choose the project's name
- **Then** its detail opens, following [[E3-F11-US01 - Open a Project's Detail]]

### Scenario: A project in error stands out

- **Given** a project in Error
- **When** I look at the grid
- **Then** its card is edged in red
- **And** in place of uptime it says what went wrong, such as how often it restarted
