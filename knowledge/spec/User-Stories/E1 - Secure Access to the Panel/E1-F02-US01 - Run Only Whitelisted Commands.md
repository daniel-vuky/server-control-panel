---
id: E1-F02-US01
type: user-story
epic: Secure Access to the Panel
feature: Command Safety
status: Draft
personas:
  - Server Owner
source: "Server Control Panel – Product Spec & E2E Flow — §3 Security 3, §4.3 Settings"
---
# E1-F02-US01 — Run Only Whitelisted Commands

## User Story

As the Server Owner, I want the panel to run only a fixed set of commands, so that no button, field or request can be turned into arbitrary shell access on my server.

## Acceptance Criteria

### Scenario: Every action maps to a known command

- **Given** any action the panel offers — start, stop, restart, backup, scan, read logs, read metrics
- **When** it runs
- **Then** the command it runs is one of a fixed list for that action and that stack
- **And** no command outside that list can be run by any means

### Scenario: Values are passed as arguments, never as shell text

- **Given** a value that came from a person or from a project, such as a project name, a container name or a path
- **When** it is used in a command
- **Then** it is passed as a separate argument
- **And** characters such as `;`, `&&`, `|` or `$()` in it have no effect beyond being part of that value

### Scenario: Custom start and stop commands come from templates

- **Given** I change how a project is started or stopped
- **When** I open the choice
- **Then** I pick from a list of templates for that stack
- **And** there is no field where a free-form command can be typed

### Scenario: There is no terminal

- **Given** any screen of the panel
- **When** I look for a way to run my own commands
- **Then** there is none — no web terminal, no console, no command box
