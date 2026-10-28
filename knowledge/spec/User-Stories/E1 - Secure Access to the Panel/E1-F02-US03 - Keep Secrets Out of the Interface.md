---
id: E1-F02-US03
type: user-story
epic: Secure Access to the Panel
feature: Command Safety
status: Draft
personas:
  - Operator
source: "Server Control Panel – Product Spec & E2E Flow — §3 Security 5"
---
# E1-F02-US03 — Keep Secrets Out of the Interface

## User Story

As an Operator, I want secret values from `.env` files and database credentials never to appear in the panel, so that signing in to the panel does not expose passwords and keys.

## Acceptance Criteria

### Scenario: Detected databases show no credentials

- **Given** a database detected from `DATABASE_URL`, `MONGO_URI`, `MYSQL_*` or `POSTGRES_*`
- **When** I look at it anywhere in the panel
- **Then** I see its type, where it runs, its name and the variable it was found in
- **And** I never see its password, and a connection string is never shown whole

### Scenario: Credentials are used only on the server side

- **Given** a backup that needs the database password
- **When** it runs
- **Then** the password is read and used by the backend only
- **And** it is never sent to the browser, written to the activity log or included in an error message

### Scenario: Logs and errors do not leak secrets the panel knows

- **Given** a step fails with an error that would contain a known secret
- **When** the error is shown or logged by the panel
- **Then** the secret is masked
