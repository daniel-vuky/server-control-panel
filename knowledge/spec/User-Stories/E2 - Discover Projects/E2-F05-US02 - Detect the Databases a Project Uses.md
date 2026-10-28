---
id: E2-F05-US02
type: user-story
epic: Discover Projects
feature: Project and Database Detection
status: Draft
personas:
  - Server Owner
source: "Server Control Panel – Product Spec & E2E Flow — §2 Stack detection rule 5, §2 Database"
---
# E2-F05-US02 — Detect the Databases a Project Uses

## User Story

As the Server Owner, I want the databases each project uses found automatically, so that I can back them up without describing each one by hand.

## Acceptance Criteria

### Scenario: A database found in environment variables

- **Given** a project whose `.env` holds `DATABASE_URL`, `MONGO_URI`, `MYSQL_*` or `POSTGRES_*`
- **When** it is scanned
- **Then** a database is recorded for it with its type, its name and where it runs
- **And** the variable it was found in is recorded, but not its secret value, following [[E1-F02-US03 - Keep Secrets Out of the Interface]]

### Scenario: A database found as a compose service

- **Given** a compose project with a service using a `postgres`, `mysql`, `mariadb` or `mongo` image
- **When** it is scanned
- **Then** a database is recorded for that service
- **And** it is marked as running in a container, with the container named

### Scenario: The supported types

- **Given** any detected database
- **When** its type is recorded
- **Then** it is one of PostgreSQL, MySQL (including MariaDB), MongoDB or SQLite

### Scenario: More than one database

- **Given** a project using two databases
- **When** it is scanned
- **Then** both are recorded, each with its own backup settings

### Scenario: No database found

- **Given** a project where no database is found
- **When** I open its backups
- **Then** I am told no database was detected and offered [[E5-F17-US03 - Add a Database Manually]]
