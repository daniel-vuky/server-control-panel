---
id: E1-F01-US01
type: user-story
epic: Secure Access to the Panel
feature: Sign-in
status: Draft
personas:
  - Operator
source: "Server Control Panel – Product Spec & E2E Flow — §3 Security 1, Flow A"
---
# E1-F01-US01 — Sign In Through Cloudflare Access

## User Story

As an Operator, I want to reach the panel only after signing in through Cloudflare Access, so that nobody outside the team can see or control the server.

## Acceptance Criteria

### Scenario: An allowed email signs in with Google

- **Given** my email is allowed by the Cloudflare Access policy, which is the only allow list ([ADR 0006](../../../../docs/decisions/0006-authenticate-through-cloudflare-access-and-bind-to-loopback.md))
- **When** I open `panel.<domain>` and sign in with that Google account
- **Then** I reach the panel
- **And** the panel knows who I am from the identity Cloudflare Access passes to it

### Scenario: An email that is not allowed is stopped before the panel

- **Given** a Google account whose email the Cloudflare Access policy does not allow
- **When** someone tries to sign in with it
- **Then** they are stopped at the Cloudflare Access sign-in page
- **And** no page, data or request of the panel is reached

### Scenario: The panel cannot be reached around Cloudflare Access

- **Given** the panel is running on the server
- **When** anyone tries to reach it by the server's IP address or any port
- **Then** nothing answers, because the panel listens on `127.0.0.1` only
- **And** the Cloudflare Tunnel is the only way in

### Scenario: A request arriving without a signed-in identity is refused

- **Given** a request reaches the panel without the identity Cloudflare Access attaches
- **When** the panel receives it
- **Then** the request is refused
- **And** no action is run and no data is returned
