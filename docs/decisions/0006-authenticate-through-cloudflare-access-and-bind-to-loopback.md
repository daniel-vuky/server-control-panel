# 6. Authenticate through Cloudflare Access and bind to loopback

Date: 2026-10-28

## Status

Accepted

## Context

E1-F01-US01 requires that:

- only allowed people reach the panel, signing in through Cloudflare Access;
- the panel cannot be reached around Cloudflare by IP address or port;
- a request without a signed-in identity is refused.

E1-F03-US01 requires every action to be attributed to a person. The owner wants to sign in with Google and keeps the list of allowed emails in Cloudflare. Decided in [#1](https://github.com/daniel-vuky/server-control-panel/issues/1).

### Option A: Cloudflare Access with Google, verified by the panel

- Pro: no login code in the panel.
- Pro: people not on the list are stopped before they reach the server.

### Option B: The panel runs its own Google OAuth

- Con: more code to write and secure.
- Con: the panel would face the internet through the tunnel unprotected.

### Option C: Also keep an email allow list in the panel

- Con: a second list to keep in sync. The owner chose to rely on the Cloudflare Access policy alone.

### Option D: Trust the `Cf-Access-Authenticated-User-Email` header without verification

- Con: it can be forged by anything that reaches the port.

## Decision

1. **Cloudflare Access** protects `panel.<domain>`, with **Google** as the identity provider. **The allow list lives only in the Access policy.**
2. The panel **verifies the `Cf-Access-Jwt-Assertion` JWT on every request**:
   - the signature, against the team's JWKS (`https://<team>.cloudflareaccess.com/cdn-cgi/access/certs`, cached);
   - the `aud` claim, against the application's AUD tag;
   - expiry.

   The `email` claim is the identity used for attribution. Configuration: `CF_ACCESS_TEAM_DOMAIN`, `CF_ACCESS_AUD`.
3. **Any request without a valid token gets 401**, with no data and no action. The only exception is `GET /api/health`, which returns status and version only, for container health checks.
4. The server **binds `127.0.0.1` only** and refuses to start with any other bind address. The Cloudflare Tunnel is the only way in.
5. Local development uses a fixed fake identity compiled in **only under the `dev` build tag**, so release binaries cannot bypass verification.

The verification was implemented in [#10](https://github.com/daniel-vuky/server-control-panel/issues/10) (E1-F01-US01).

## Consequences

**Easier:**

- No passwords, sessions or OAuth flows in the panel.
- Access is managed in one place, in the Cloudflare dashboard.

**Harder:**

- The panel depends on Cloudflare Access being configured correctly (team domain, AUD tag, tunnel ingress).
- Local development needs the dev build tag to get an identity.
