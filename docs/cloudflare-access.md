# Set up Cloudflare Access

This guide connects the panel to Cloudflare Access. Do the Cloudflare part whenever you like, even before the panel runs on the server. Set the panel's variables before you start a release binary.

The menu names below follow Cloudflare's documentation as of October 2026: the dashboard at <https://dash.cloudflare.com>, with Zero Trust inside it. Cloudflare renames menus from time to time. If a label differs, look for the same step under a similar name.

## What this protects

The panel has no login of its own ([ADR 0006](decisions/0006-authenticate-through-cloudflare-access-and-bind-to-loopback.md)). Cloudflare Access signs people in with Google and stops everyone else before they reach the server. The Access policy is the only allow list: the panel does not keep one.

On every request the panel checks the `Cf-Access-Jwt-Assertion` token that Cloudflare adds. It checks the signature, the audience (your application's AUD tag), the issuer (your team domain) and the expiry. A request without a valid token gets 401. The panel listens on `127.0.0.1` only, so the Cloudflare Tunnel is the only way in.

## Before you start

You need:

- a domain whose DNS is on Cloudflare;
- a Zero Trust team (its team name gives the team domain `<team>.cloudflareaccess.com`);
- a Cloudflare Tunnel with `cloudflared` running on the server, directly on the host and not in a container ([ADR 0008](decisions/0008-deploy-the-panel-as-a-docker-compose-project-with-pull-based-updates.md)).

If you already reach the server over SSH through Cloudflare, you have all three. **Reuse them**: the panel is one more route on the same tunnel and one more Access application in the same team. Do not create a second tunnel. The differences from SSH are that the route's service is `http://` instead of `ssh://`, and people open the panel in a browser, with no `cloudflared` on their machine.

Work in this order: login method, policy, Access application, and only then the tunnel route. That way `panel.<domain>` is never reachable without Access in front of it.

## 1. Add Google as a login method

Skip this section if Google is already listed under **Zero Trust** > **Integrations** > **Identity providers** (for example, because your SSH sign-in uses it).

In the Google Cloud console:

1. Create a project, or pick an existing one.
2. Go to **APIs & Services** > **Credentials** and select **Configure Consent Screen**. Select **Get started**, then enter an **App name** and a **User support email**. Choose **External** as the audience, enter your contact information, accept the policy and select **Create**.
3. On the OAuth overview page, select **Create OAuth client**. Choose **Web application** as the application type, and give it a name.
4. Under **Authorized JavaScript origins**, add `https://<team>.cloudflareaccess.com`.
5. Under **Authorized redirect URIs**, add `https://<team>.cloudflareaccess.com/cdn-cgi/access/callback`.
6. Save. Then copy the **Client ID** and the **Client secret**.

In Cloudflare:

1. Go to **Zero Trust** > **Integrations** > **Identity providers**.
2. Under **Your identity providers**, select **Add new identity provider**, then **Google**.
3. Paste the Client ID into **App ID**, and the client secret into **Client secret**. Select **Save**.
4. Back in the list, select **Test** next to Google and sign in once to check it.

Your team name is under **Zero Trust** > **Settings** > **Team name and domain**.

## 2. Create the allow policy

Skip this if your SSH application already has a policy listing the same people. You can reuse it.

1. Go to **Zero Trust** > **Access controls** > **Policies** and select **Add a policy**.
2. **Policy name**: for example `Panel operators`.
3. **Action**: **Allow**.
4. **Session duration**: how long a sign-in lasts, for example 24 hours.
5. Add an **Include** rule with the **Emails** selector. Enter every email that may use the panel, one per value.
6. Select **Save**.

Anyone not on the list is stopped at the Cloudflare sign-in page. Applications deny by default, so this Allow policy is what lets your team in.

## 3. Create the Access application

1. Go to **Zero Trust** > **Access controls** > **Applications** and select **Create new application**.
2. Choose **Self-hosted and private**.
3. Select **Add public hostname**. Enter `panel` as the subdomain and choose your domain in the **Domain** list.
4. Under **Access policies**, add the policy from section 2.
5. Under login methods, select **Google**. With Google as the only method you can turn on **Apply instant authentication**, so people go straight to Google without the Cloudflare login page.
6. Set the **Session Duration** you want.
7. Select **Create**.

## 4. Find the two values the panel needs

- **Team domain.** **Zero Trust** > **Settings** > **Team name and domain**. The team domain is `<team>.cloudflareaccess.com`.
- **AUD tag.** **Zero Trust** > **Access controls** > **Applications**, then select **Configure** on the panel application. Under **Additional settings**, copy the **Application Audience (AUD) Tag**. It is a long hex string.

## 5. Route the panel through the tunnel

Use the method your tunnel already uses. If you created it in the dashboard, it is remotely managed. If you run it from a `config.yml` on the server, it is locally managed.

**Remotely managed (dashboard):**

1. Go to **Networking** > **Tunnels** and select the tunnel your SSH already uses.
2. On the **Routes** tab, select **Add route**, then **Published application**.
3. Enter `panel` as the subdomain and choose your domain.
4. In **Service URL**, enter `http://127.0.0.1:8471`.
5. Select **Add route**. Cloudflare creates the DNS record for `panel.<domain>`.

**Locally managed (`config.yml`):**

1. Add the panel above the catch-all rule in the tunnel's `config.yml`. The catch-all must stay last:

   ```yaml
   ingress:
     - hostname: ssh.<domain>        # your existing SSH rule, unchanged
       service: ssh://localhost:22
     - hostname: panel.<domain>
       service: http://127.0.0.1:8471
     - service: http_status:404
   ```

2. Check the rules with `cloudflared tunnel ingress validate`.
3. Create the DNS record with `cloudflared tunnel route dns <tunnel name or UUID> panel.<domain>`.
4. Restart `cloudflared` so it reads the new file. If it runs as a service, use `sudo systemctl restart cloudflared`.

Use the port from `PANEL_ADDR` if you changed it. The default is `8471`. Until the panel runs on the server, `panel.<domain>` shows a Cloudflare 502 error after sign-in. That is expected.

## 6. Configure the panel

Set these variables where the panel runs.

| Variable | Value | Notes |
| --- | --- | --- |
| `CF_ACCESS_TEAM_DOMAIN` | `<team>.cloudflareaccess.com` | A leading `https://` and a trailing `/` are accepted. Letters are lower-cased. Anything that is not a `cloudflareaccess.com` team domain is refused. |
| `CF_ACCESS_AUD` | the AUD tag | Spaces around it are trimmed and letters are lower-cased. Only hex characters are accepted. |
| `PANEL_ADDR` | `127.0.0.1:8471` | Optional. Must be `127.0.0.1` with a port. Any other address is refused. |

A release binary with a missing or malformed `CF_ACCESS_TEAM_DOMAIN` or `CF_ACCESS_AUD` exits at startup and names the variable. This is on purpose: the panel never runs unprotected.

Dev builds (`make dev`) ignore these variables and use the fixed identity `dev@localhost`.

Packaging and updates are not covered here. They are tracked in [#75](https://github.com/daniel-vuky/server-control-panel/issues/75).

## Troubleshooting

Every 401 is logged as a `request refused` warning with the `path` and a `reason`. The token is never logged.

- `no token`: the request had no `Cf-Access-Jwt-Assertion` header, or an empty one. It did not come through Cloudflare Access. Check that the tunnel route and the Access application use the same hostname.
- `expected audience`: `CF_ACCESS_AUD` does not match the application the request came through. Copy the AUD tag again (section 4).
- An issuer error: `CF_ACCESS_TEAM_DOMAIN` does not match the team that signed the token.
- A key set error: the panel could not fetch the team's keys from `https://<team>.cloudflareaccess.com/cdn-cgi/access/certs`. Check the server's outbound network and the team domain. The panel retries at most once every 30 seconds.
- An expired token: sign in again. Sessions run out after the session duration.
- `token has no email`: service tokens carry no email, so they are refused in v1.

## Manual acceptance checklist

Item 2 works as soon as sections 1–5 are done. The others need the panel running on the server. Together they cover the four scenarios in [#10](https://github.com/daniel-vuky/server-control-panel/issues/10).

- [ ] An allowed Google account opens `panel.<domain>`, reaches the panel and sees its email in the sidebar.
- [ ] A Google account that is not allowed stops at the Cloudflare sign-in page and reaches nothing of the panel.
- [ ] `curl http://<server-ip>:8471` from another machine fails with "connection refused" or times out. Nothing answers.
- [ ] On the server, `curl -H 'Cf-Access-Jwt-Assertion: x' http://127.0.0.1:8471/api/me` returns 401.
