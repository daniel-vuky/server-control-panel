---
id: E3-F11-US04
type: user-story
epic: Monitor Projects
feature: Project Detail
status: Draft
personas:
  - Operator
source: "Server Control Panel – Product Spec & E2E Flow — §4.3 Logs, Flow C step 3"
---
# E3-F11-US04 — Read Live Logs

## User Story

As an Operator, I want to read a project's logs live in the panel, so that I can find out why it failed without opening a terminal.

## Acceptance Criteria

### Scenario: The latest lines, live

- **Given** a project's Logs tab
- **When** I open it
- **Then** I see its 500 most recent log lines
- **And** new lines appear at the bottom as they are written

### Scenario: Choosing a container or process

- **Given** a project with more than one container or process
- **When** I choose one
- **Then** only its lines are shown
- **And** I can also choose all of them together

### Scenario: Pausing the scroll

- **Given** lines are arriving
- **When** I choose **Pause scrolling**
- **Then** the view stops moving so I can read
- **And** **Resume scrolling** jumps to the newest line

### Scenario: Searching

- **Given** the log view
- **When** I type in its search box
- **Then** matching lines are shown and the match is highlighted

### Scenario: Downloading

- **Given** the log view
- **When** I choose **Download**
- **Then** the lines currently held are saved as a text file named after the project and the time

### Scenario: Levels are readable

- **Given** lines with a level such as INFO, WARN or ERROR
- **When** they are shown
- **Then** each level has its own colour and its word
