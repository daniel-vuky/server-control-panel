---
id: E3-F11-US03
type: user-story
epic: Monitor Projects
feature: Project Detail
status: Draft
personas:
  - Operator
source: "Server Control Panel – Product Spec & E2E Flow — §4.3 Overview"
---
# E3-F11-US03 — Chart a Project's Resource Usage

## User Story

As an Operator, I want charts of one project's CPU, RAM and network over time, so that I can see whether it is growing, spiking or leaking.

## Acceptance Criteria

### Scenario: Three charts

- **Given** a project's Overview
- **When** I look at its resources
- **Then** I see charts of its CPU, its RAM and its network in and out, each with its current value

### Scenario: Choosing a period

- **Given** the charts
- **When** I choose 1 hour, 24 hours or 7 days
- **Then** all three charts show that period
- **And** the time axis says which period is shown

### Scenario: No data for part of the period

- **Given** a period that includes time when the project was stopped or the panel was not sampling
- **When** the chart is drawn
- **Then** that time is shown as a gap, not as zero

### Scenario: History beyond retention

- **Given** a period longer than the metrics kept, following [[E6-F20-US01 - Sample and Retain Metrics]]
- **When** the chart is drawn
- **Then** it shows what is kept and says where the history begins
