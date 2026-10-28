---
id: E5-F15-US01
type: user-story
epic: Back Up Databases
feature: Manual Backup
status: Draft
personas:
  - Operator
source: "Server Control Panel – Product Spec & E2E Flow — Flow E steps 1–2, 4"
---
# E5-F15-US01 — Back Up a Database Now

## User Story

As an Operator, I want to back up a project's database on demand and have it land on Google Drive, so that I have a fresh copy before a risky change.

## Acceptance Criteria

### Scenario: Starting a backup

- **Given** a project with one detected database
- **When** I choose **Back up now** on its card, its detail or its Database & Backup tab
- **Then** a backup of that database starts

### Scenario: Choosing among several databases

- **Given** a project with more than one database
- **When** I choose **Back up now**
- **Then** I am asked which database to back up, each shown with its type and where it runs

### Scenario: Drive is not connected

- **Given** Google Drive is not connected
- **When** I choose **Back up now**
- **Then** I am invited to connect it, following [[E5-F14-US01 - Connect Google Drive]]
- **And** once connected, I return to the backup I started

### Scenario: What a backup does

- **Given** a backup has started
- **When** it runs
- **Then** the database is dumped with the tool for its type — `pg_dump`, `mysqldump` or `mongodump` — inside its container if it runs in Docker
- **And** the dump is compressed with gzip and named `<project>_<db>_<YYYYMMDD-HHmm>.sql.gz`
- **And** it is uploaded to `<folder>/<project>/` on Drive
- **And** the temporary file on the server is deleted

### Scenario: A finished backup is recorded

- **Given** a backup has finished
- **When** I look at the project's backup history
- **Then** a new row shows its time, size, duration, result, a link to the file on Drive and who started it
