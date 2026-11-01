# 12. Send alerts directly from the panel

Date: 2026-11-01

## Status

Accepted

## Context

The panel watches the server every 30 seconds and its projects every 5 while a tab is open, but it told nobody unless someone looked. A crashed project, a full disk or a failed 02:00 backup showed only when the owner opened it ([#64](https://github.com/daniel-vuky/server-control-panel/issues/64), [#65](https://github.com/daniel-vuky/server-control-panel/issues/65), [#66](https://github.com/daniel-vuky/server-control-panel/issues/66), [#67](https://github.com/daniel-vuky/server-control-panel/issues/67), [#68](https://github.com/daniel-vuky/server-control-panel/issues/68), [#69](https://github.com/daniel-vuky/server-control-panel/issues/69), [#70](https://github.com/daniel-vuky/server-control-panel/issues/70)). The first channels are Telegram and email.

A message has to leave the server when the thing that broke is the server's own path to the internet. The panel is reached through a Cloudflare Tunnel ([ADR 6](0006-authenticate-through-cloudflare-access-and-bind-to-loopback.md)): when the tunnel drops, the panel cannot be opened, and an alert that went through the tunnel would be lost with it.

### Option A: A relay or monitoring service

An external service (Uptime Kuma, a hosted monitor, an alert manager) receives the panel's events and notifies the owner.

- Pro: retries, escalation and channels come for free.
- Con: one more thing to install, run and secure, for a panel that is meant to be one binary and one compose project ([ADR 8](0008-deploy-the-panel-as-a-docker-compose-project-with-pull-based-updates.md)).
- Con: the panel would still have to send the events somewhere, so a destination, a credential and a failure mode are added without removing any.

### Option B: Send from the panel process

The panel calls the Telegram Bot API over HTTPS and an SMTP server itself, from its own process, on the host's network (the panel container uses host networking).

- Pro: nothing to run besides the panel; an alert does not depend on the tunnel, the Access policy or any service the owner has to keep up.
- Pro: the panel already knows what is wrong and can say it, with the project, the cause and a link.
- Con: the panel makes outbound requests to the internet, and holds a bot token.

## Decision

Option B, with these limits.

1. **Two destinations and one rule.** The panel sends to `api.telegram.org` over HTTPS and to the SMTP server named in `panel.env`, and nowhere else for alerts. No alert goes through the Cloudflare Tunnel, which only carries requests into the panel. When the tunnel is down the alert about it still leaves, because it takes the host's own network.
2. **The bot token is sealed and never shown.** It is entered in Settings, sealed with `PANEL_SECRET_KEY` (the same `secret.Seal` as the Drive token) and opened only inside the send. Without a key Telegram cannot be saved. The API returns a hint (the first three characters after the colon) and the chat id masked to its sign, a `-100` prefix if it has one and its last four digits (`-100••••4821`). The request URL contains the token and Go's `*url.Error` prints it, so the sender never wraps or logs the client's error: it classifies the cause (timeout, dial, TLS) and returns fixed words with a code. The client follows no redirect (a redirect would resend the token) and uses no proxy.
3. **The SMTP server lives in `panel.env`, only the recipients in Settings.** `SMTP_HOST`, `SMTP_PORT`, `SMTP_USERNAME`, `SMTP_PASSWORD` and `SMTP_FROM`, the way the Google client secret is: a password the owner types into a web page is a password the panel has to store, and the key that seals it is the same file. Port 465 is implicit TLS; any other port requires STARTTLS with a verified certificate, and the panel refuses to send without TLS unless the host is `localhost`, `127.0.0.1` or `::1` (a local relay). `install.sh` does not ask for the five values: an optional channel is not worth five more prompts, and `docs/alerts.md` says what to add.
4. **Domain checks.** A project with a public domain is checked every 60 seconds while it is Running, Partial or Error, with one `GET https://<domain>/` on port 443: 10 second timeout, TLS verified, no redirect followed, no proxy, body not read. Any status below 500 means the site answers (a 302 to the Access login or a 404 is an answer); a timeout, a connection or TLS error or a 5xx is a failure, and three in a row raise the alert. The domain is typed by the owner behind Access, and the panel's own field check accepts a bare IP such as `10.0.0.1`, so the request can reach an address inside the host's network. That is accepted without an IP filter: the request is a verified-TLS GET on port 443 whose only output is "answered" or "did not answer", with no body, header or status shown anywhere, so it reads nothing an owner who can edit the project could not already reach.
5. **One notifier, one worker, one rate cap.** Callers (the watcher, the backup manager) only push onto an in-memory queue, so none waits on the store or the network. The worker records each alert in `activity`, sends it to every enabled channel, retries a network failure once after 30 seconds and finishes the row as Alert sent, Not delivered or Not sent. At most 10 alerts leave the panel in 5 minutes, so a storm (every domain failing at once) cannot flood the owner; the rest are recorded as Not sent.
6. **Each problem alerts once.** An alert opens an incident, stored in `alert_incidents` because every release restarts the panel; nothing more is sent for that problem until it clears, and then one recovery message follows. Scheduled-backup failures are separate events with no recovery.
7. **No process, no new Docker read.** The watcher reads Docker only through the hub's cache and metrics only through the Monitor; `internal/runner` is still the only package that starts a process.

## Consequences

**Easier:**

- Alerts work with nothing to install: a bot token and a chat id, or an SMTP account the owner already has.
- The tunnel alert is not lost to the tunnel.
- The Activity page shows every alert and whether it was delivered, next to what people did.

**Harder:**

- The panel now makes outbound requests: HTTPS to `api.telegram.org`, the SMTP host and port, and port 443 of each public domain. A host that blocks them sends nothing, and the Activity row says Not delivered with the reason.
- The panel holds a bot token. It is sealed at rest and absent from the API, logs and errors, but anyone with the sealed value and `PANEL_SECRET_KEY` can use it. Revoke it in BotFather if the server is lost.
- Two tries per channel and no later retry: a channel down for a long time loses the alert (the incident stays open, so it is not sent again). The owner sees it in Activity.
- An email needs the SMTP values in `panel.env` and `docker compose up -d` before its Settings row can be set up.
- Alerts for a project stopped through the panel are never sent, and an OOM-killed container (exit code 137, like 143) reads Stopped, not Error, so it raises no Error alert. That is the existing status rule, not a new one.
