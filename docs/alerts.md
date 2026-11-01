# Alerts

The panel tells you when something is wrong, so you do not have to open it to find out. It sends a message to Telegram, to email or to both, and keeps a record of every alert and whether it was delivered on the **Activity** page. This guide sets up the two channels and says what each alert means.

Everything here is set up in **Settings → Alerts**, except the email server, which goes in `panel.env`.

## How alerts leave the server

The panel sends alerts itself, from the server, over the server's own network: HTTPS to `api.telegram.org`, and the SMTP server you name. Nothing goes through the Cloudflare Tunnel, so an alert that the tunnel dropped still reaches you. The server therefore needs outbound access to those two destinations (and, for the domain checks below, to port 443 of your public domains).

The panel never shows the bot token again after you save it: Settings shows only its first few characters and the chat id with the middle hidden. The token is stored encrypted with `PANEL_SECRET_KEY`, so Telegram cannot be saved while that variable is missing ([deploy guide, section 4a](deploy.md#4a-the-secret-key-and-the-google-drive-settings)).

## Telegram

You need a bot and the number of the chat it should write to.

### 1. Create the bot and get its token

1. In Telegram, open a chat with `@BotFather` and send `/newbot`.
2. Give the bot a name and a username that ends in `bot`.
3. BotFather answers with a token that looks like `123456789:AAH...` (digits, a colon, then at least 35 letters, digits, `_` or `-`). Copy it. Treat it like a password: anyone who has it can send messages as your bot.

### 2. Find the chat id

The panel needs the **numeric** chat id. A channel name such as `@mychannel` is not accepted.

1. Open your new bot in Telegram and send it any message, for example `hello`. For a group, add the bot to the group and send a message there. For a channel, add the bot as an administrator and post a message.
2. In a browser, open `https://api.telegram.org/bot<token>/getUpdates`, with your token in place of `<token>` (keep the word `bot` before it).
3. Find `"chat":{"id":...` in the answer. A private chat is a positive number such as `123456789`; a group or channel is negative, usually starting with `-100`, such as `-1001234567890`. Copy the whole number including the minus sign.

If the answer is `{"ok":true,"result":[]}`, Telegram has no message for the bot yet: send another message to it and reload.

That page shows your token in its address, so do not paste it into chats or tickets. Close the tab when you are done.

### 3. Set it up

1. In **Settings → Alerts**, choose **Set up** on the Telegram row.
2. Paste the **Bot token** and the **Chat ID** and save.
3. Choose **Send test**. The row answers "Delivered", or says why not: "Telegram refused the bot token.", "Chat not found: send the bot a message first, or add it to the group.", "The bot was blocked or removed from the chat." or "Could not reach Telegram.".

To change the chat later, choose **Edit** and leave the token empty to keep the stored one.

## Email

Email needs a mail server (SMTP) that accepts the panel's login. The server's details go in `panel.env`, like the Google client secret: the password is never typed into the panel. Only the recipients are entered in Settings.

### 1. Add the SMTP values to `panel.env`

Open `/opt/server-control-panel/panel.env` and add:

```text
SMTP_HOST=smtp.example.com
SMTP_PORT=587
SMTP_USERNAME=alerts@example.com
SMTP_PASSWORD=<the account's password or app password>
SMTP_FROM=Server Control Panel <alerts@example.com>
```

- `SMTP_HOST` and `SMTP_FROM` are required together. Leave **every** `SMTP_*` variable empty to leave email off. Any other `SMTP_*` value without a host, a bad port, or a host without `SMTP_FROM`, stops the panel at start-up with a message naming the variable.
- `SMTP_PORT` is optional and defaults to 587. Values are trimmed of spaces around them.
- `SMTP_USERNAME` and `SMTP_PASSWORD` are optional (a local relay may need no login). With a username the panel logs in with PLAIN authentication, and refuses to do it to a remote server without TLS.
- `SMTP_FROM` is the sender, a plain address or `Name <address>`.

Transport security: port **465** uses TLS from the first byte. Any other port must offer **STARTTLS** and the panel verifies the server's certificate; it refuses to send without TLS, except to `localhost`, `127.0.0.1` or `::1` (a relay on the same server). Many providers want an app password rather than your account password.

The install does not ask for these values. After editing `panel.env`, apply them:

```
cd /opt/server-control-panel && docker compose up -d
```

The panel restarts to read the file. Until the values are there, the Email row says "Set SMTP_HOST and SMTP_FROM in panel.env" and cannot be set up.

### 2. Set up the recipients

1. In **Settings → Alerts**, choose **Set up** on the Email row.
2. Enter 1 to 10 addresses, separated by commas or new lines, and save.
3. Choose **Send test**. Failures are worded: "The mail server refused the login.", "Could not reach the mail server." or "The mail server refused <address>.".

The subject is `[<server name>] <alert title>` and the body is plain text.

## The on/off switch and Send test

A channel starts **on** when you set it up. Turning it off stops the panel from sending through it, but alerts are still recorded in Activity (as Not sent when no channel is on). **Send test** works whether the channel is on or off. A test is not an alert and is not recorded.

## What the panel alerts on

Each message has a title, a detail line, then the server name and the time in the server's time zone, and a link to the project or page when `PANEL_PUBLIC_URL` is set.

Each problem alerts **once**. While it lasts nothing more is sent for it; when it clears the panel sends one recovery message (a green check) and the problem is closed. Open problems are remembered across restarts, so an update that restarts the panel does not repeat an alert for a project that is still in Error.

| Alert | When it is sent | Recovery |
| --- | --- | --- |
| **CPU, RAM** of the server | The value stays **above** the threshold for the "Sustained for" time (default 5 minutes). Example: "CPU at 92 % for 5 min (threshold 90 %)". | "CPU back to 41 % (threshold 90 %)" on the first reading at or under the threshold. |
| **Disk** | A disk is above the disk threshold for the same time, one alert per disk. Example: "Disk /var at 91 % (threshold 85 %)". | "Disk /var back to 80 %" when it is at or under. |
| **A project's CPU or RAM** | The project is above its own threshold from its Settings tab, or above the server-wide percentage if it has none, for the same time. A project's CPU is a share of the whole server; its RAM is in MB. | Back under the threshold. |
| **Cloudflare Tunnel** | The tunnel was seen connected before and then reads disconnected or not running on **two checks in a row** (about 30 to 60 seconds, so a brief reconnect does not page you). A server that never had a tunnel never alerts. | "Cloudflare Tunnel connected again". |
| **A project is in Error** | The project reads Error on two checks at least 30 seconds apart, for example a container that exited with an error code or keeps restarting. The message names the cause: the highest restart count, else the first exit reason, else "R of N containers running". | "<project> is running again" when it reads Running or Partial. A project that goes Stopped, is not found or is untracked closes the problem silently. |
| **A public domain does not answer** | `https://<domain>/` fails **3 checks in a row** (one check a minute, only while the project is Running, Partial or Error). | "<domain> is answering again" on the first answer. |
| **A scheduled backup fails** | A scheduled run fails or is refused. The message names the step, the reason and the hint. | None: each failed backup is its own event. |

Details worth knowing:

- "Over" means strictly above the threshold. The thresholds are in Settings → Alerts: CPU, RAM and disk percent (1 to 100) and "Sustained for" (1 to 60 minutes). The same values colour the Dashboard tiles and draw the lines on the Server charts.
- **A project stopped through the panel never alerts.** A project that goes Stopped closes its Error and domain problems silently, and a stopped project's domain is not checked. A container killed for running out of memory exits with code 137 (and `docker stop` with 143), which the panel reads as Stopped, so an OOM-kill does **not** raise an Error alert.
- **Domain checks** send one HTTPS GET to port 443 with a 10 second timeout, verify the certificate and follow no redirect. Any answer below 500 counts as the site answering (a redirect to the Access login or a 404 is an answer); a timeout, a connection or certificate error, or a 5xx counts as a failure. While the tunnel was seen connected and now reads down, domain failures are not counted, because the tunnel alert already names the cause.
- **Scheduled backups only.** A backup someone starts never alerts. A scheduled run that fails or is refused does. An **interrupted** one (the panel was restarted while it ran, which every update does) never alerts, and neither does a run the scheduler skipped without storing a row (Drive not set up, a database that cannot be backed up, a backup of it already running).
- A Docker that is down is not an alert yet.

## The limit: 10 alerts in 5 minutes

At most **10 alerts leave the panel in any 5 minutes**, so a bad moment (every domain failing at once) does not flood your phone. An alert beyond that is recorded in Activity as Not sent ("Too many alerts in 5 minutes; see Activity.") and nothing else is sent. Recoveries count toward the limit; a recovery dropped by it still closes its problem.

## Where to see what happened

**Activity** in the sidebar lists every alert with the channels it went to and its result:

- **Alert sent**: delivered to at least one channel (the line says which, and names a channel that failed).
- **Not delivered**: every channel that was on failed. The reason is on the line. The panel tries a network failure twice, 30 seconds apart, and then gives up; the problem stays open, so it is not sent again.
- **Not sent**: no channel was on, or the limit above was reached.

Activity also lists backups and what people did, with chips for All, Actions, Backups and Alerts and a project filter.

## Troubleshooting

| Symptom | What to do |
| --- | --- |
| Settings says "Set PANEL_SECRET_KEY in panel.env first." | Telegram cannot be saved without the key. Run the install command again: it appends a key to an older `panel.env`. |
| The Email row says to set `SMTP_HOST` and `SMTP_FROM` | Add the variables above and run `docker compose up -d`. |
| The panel will not start after editing `panel.env` | The log names the `SMTP_*` variable. Fix or empty it. |
| Test says "Telegram refused the bot token." | The token is wrong or was revoked in BotFather. Choose Edit and enter it again. |
| Test says "Chat not found" | Send your bot a message first (or add it to the group), then check the chat id, including the minus sign for a group. |
| Test says "Could not reach the mail server." | Check `SMTP_HOST` and `SMTP_PORT`, and that the server allows outbound connections on that port. |
| Alerts show Not delivered in Activity | Open the row: the reason names the channel and the cause. Use Send test to check the channel again. |
