---
id: E8-F24-US01
type: user-story
epic: Provide Consistent Interface Behavior
feature: Screen States
status: Draft
personas:
  - Operator
source: "Server Control Panel – Product Spec & E2E Flow — §7 Interface principles, §6 Empty states"
---
# E8-F24-US01 — Show Loading, Empty and Error States on Every Screen

## User Story

As an Operator, I want every screen to look deliberate while loading, when empty and when something fails, so that I always know whether I am waiting, done or blocked.

## Acceptance Criteria

### Scenario: Loading

- **Given** any screen whose data has not arrived yet
- **When** it is shown
- **Then** grey placeholders shaped like the content are shown in its place
- **And** no zero or blank value is shown as if it were real

### Scenario: Empty

- **Given** any screen with nothing to show
- **When** it is shown
- **Then** it says why it is empty and offers the next step:

| Situation | Message | Next step |
|---|---|---|
| No workspace yet | No workspace has been added | **Add project folder** |
| Scan found nothing | No project was found | Scan deeper, or check the path |
| Project has no database | No database detected | **Add database manually** |
| Google Drive not connected | Banner on Backups | **Connect Google Drive** |
| Database never backed up | No backup yet | **Back up now** |

### Scenario: Error

- **Given** any screen whose data could not be loaded
- **When** it is shown
- **Then** it says what failed in plain words and offers **Retry**
- **And** data already shown is kept, dimmed, rather than wiped

### Scenario: Data

- **Given** any screen whose data has arrived
- **When** it is shown
- **Then** it shows the data, with no placeholder left behind
