# Set up Cloudflare Access

This guide connects the panel to Cloudflare Access. Do it once, before you run a release build. Cloudflare's dashboard changes over time, so menu names here may differ a little.

## What this protects

The panel has no login of its own ([ADR 0006](decisions/0006-authenticate-through-cloudflare-access-and-bind-to-loopback.md)). Cloudflare Access signs people in with Google and stops everyone else before they reach the server. The Access policy is the only allow list: the panel does not keep one.

On every request the panel checks the `Cf-Access-Jwt-Assertion` token that Cloudflare adds. It checks the signature, the audience (your application's AUD tag), the issuer (your team domain) and the expiry. A request without a valid token gets 401. The panel listens on `127.0.0.1` only, so the Cloudflare Tunnel is the only way in.

## Create the Access application

1. Open the Cloudflare Zero Trust dashboard.
2. Go to Access, then Applications.
3. Add a self-hosted application for `panel.<domain>`.
4. Add Google as the login method (identity provider), if it is not set up yet.
5. Add an Allow policy that lists the emails of the people who may use the panel. Anyone not listed is stopped at the Cloudflare sign-in page.

## Find the values

- **Team domain.** In Zero Trust, open Settings. Your team name is under Custom Pages or the general settings. The team domain is `<team>.cloudflareaccess.com`.
- **AUD tag.** Open the application you created. The AUD tag is on its Overview page. It is a long hex string.

## Point the tunnel at the panel

`cloudflared` runs on the host, not in a container ([ADR 0008](decisions/0008-deploy-the-panel-as-a-docker-compose-project-with-pull-based-updates.md)). Add an ingress rule for the panel:

```yaml
ingress:
  - hostname: panel.<domain>
    service: http://127.0.0.1:8471
  - service: http_status:404
```

Use the port from `PANEL_ADDR` if you changed it. The default is `8471`.

## Configure the panel

Set these three variables where the panel runs.

| Variable | Value | Notes |
| --- | --- | --- |
| `CF_ACCESS_TEAM_DOMAIN` | `<team>.cloudflareaccess.com` | A leading `https://` and a trailing `/` are accepted. Letters are lower-cased. Anything that is not a `cloudflareaccess.com` team domain is refused. |
| `CF_ACCESS_AUD` | the AUD tag | Spaces around it are trimmed. Only hex characters are accepted. |
| `PANEL_ADDR` | `127.0.0.1:8471` | Optional. Must be `127.0.0.1` with a port. Any other address is refused. |

A release build that has a missing or malformed `CF_ACCESS_TEAM_DOMAIN` or `CF_ACCESS_AUD` exits at startup and names the variable. This is on purpose: the panel never runs unprotected.

Dev builds (`make dev`) ignore these variables and use the fixed identity `dev@localhost`.

Packaging and updates are not covered here. They are tracked in [#75](https://github.com/daniel-vuky/server-control-panel/issues/75).

## Troubleshooting

Every 401 is logged as a `request refused` warning with the `path` and a `reason`. The token is never logged.

- `no token`: the request had no `Cf-Access-Jwt-Assertion` header. It did not come through Cloudflare Access.
- `expected audience`: `CF_ACCESS_AUD` does not match the application the request came through. Copy the AUD tag again from the application's Overview.
- An issuer error: `CF_ACCESS_TEAM_DOMAIN` does not match the team that signed the token.
- A key set error: the panel could not fetch the team's keys from `https://<team>.cloudflareaccess.com/cdn-cgi/access/certs`. Check the server's outbound network and the team domain. The panel retries at most once every 30 seconds.
- An expired token: sign in again. Sessions run out.
- `token has no email`: service tokens carry no email, so they are refused in v1.

## Manual acceptance checklist

Run these once the panel is deployed. They cover the four scenarios in [#10](https://github.com/daniel-vuky/server-control-panel/issues/10).

- [ ] An allowed Google account opens `panel.<domain>`, reaches the panel and sees its email in the sidebar.
- [ ] A Google account that is not allowed stops at the Cloudflare sign-in page and reaches nothing of the panel.
- [ ] `curl http://<server-ip>:8471` from another machine gets no answer.
- [ ] On the server, `curl -H 'Cf-Access-Jwt-Assertion: x' http://127.0.0.1:8471/api/me` returns 401.
