# Connect Google Drive

This guide sets up Google Drive so the panel can store database backups there. You do the Google part once, in Google Cloud, then give the panel three values. Without them the panel runs as before and Settings shows Google Drive as not set up.

The menu names below follow Google Cloud's console as of October 2026 (**Google Auth Platform**, with Branding, Audience, Data Access and Clients). Google renames menus from time to time. If a label differs, look for the same step under a similar name.

## What the panel can see

The panel asks Google for one permission: the `drive.file` scope, shown on the consent page as access to "files this app creates". The panel can only see files and folders it creates itself. It cannot list your other folders or open your other files, and it never sees your password. The panel creates its own destination folder in My Drive (named `server-backups` by default), and the backups go into sub-folders of it, one per project.

The panel stores the refresh token Google gives it, encrypted with `PANEL_SECRET_KEY` ([deploy guide, section 4a](deploy.md#4a-the-secret-key-and-the-google-drive-settings)). Disconnecting from Settings revokes it and deletes it.

## Before you start

You need:

- the panel installed and reachable at its public address, behind Cloudflare Access ([Cloudflare Access guide](cloudflare-access.md)). Google sends your browser back to the panel, so the address must work in the browser you use;
- a Google account that will own the backup files. A personal account is fine, and so is a Google Workspace account.

Write down the panel's public address, `https://<your panel domain>`, with no path and no trailing slash. This guide calls it `<PANEL_PUBLIC_URL>`.

## 1. Create a Google Cloud project

1. Open <https://console.cloud.google.com> and sign in with the account that should own the backups.
2. In the project picker at the top, choose **New project**. Name it for example `server-control-panel` and create it.
3. Make sure the new project is the selected one.

## 2. Enable the Drive API

1. Open **APIs & Services** > **Library**.
2. Search for **Google Drive API**, open it and choose **Enable**.

## 3. Set up the consent screen and publish it

1. Open **Google Auth Platform** (or **APIs & Services** > **OAuth consent screen**) and choose **Get started** if it asks.
2. **Branding**: an app name (for example `Server Control Panel`), your email as the support email and the developer contact.
3. **Audience**: choose **External**. (If your Google account belongs to a Google Workspace organisation and only its users will ever connect, **Internal** works too, and you can skip the publishing step below.)
4. **Data Access**: choose **Add or remove scopes** and add `.../auth/drive.file` ("See, edit, create and delete only the specific Google Drive files you use with this app"). Save.
5. Back on **Audience**, under **Publishing status**, choose **Publish app** and confirm. The status must read **In production**.

**Do not leave the app in Testing.** Google expires the refresh tokens of an External app in Testing after 7 days, so the panel would lose its access every week and show "The Google Drive token has expired". `drive.file` is a non-sensitive scope, so publishing needs no Google review and no verification, and you do not have to submit anything.

## 4. Create the OAuth client

1. Open **Clients** (or **APIs & Services** > **Credentials** > **Create credentials** > **OAuth client ID**) and choose **Create client**.
2. **Application type**: **Web application**. Name it for example `panel`.
3. Under **Authorized redirect URIs** add exactly:

   ```
   <PANEL_PUBLIC_URL>/api/drive/callback
   ```

   For example `https://panel.example.com/api/drive/callback`. It must match the panel's address character for character: the same `https`, the same host, no trailing slash. A mismatch makes Google stop with the error `redirect_uri_mismatch`. Leave **Authorized JavaScript origins** empty.
4. Create the client and copy the **Client ID** and the **Client secret**.

## 5. Give the values to the panel

Open `panel.env` on the server (`/opt/server-control-panel/panel.env`) and set the three variables. If you gave them during the install they are there already, so only check them:

```
PANEL_PUBLIC_URL=https://panel.example.com
GOOGLE_CLIENT_ID=1234567890-abc.apps.googleusercontent.com
GOOGLE_CLIENT_SECRET=GOCSPX-...
```

`PANEL_PUBLIC_URL` and `<PANEL_PUBLIC_URL>/api/drive/callback` in step 4 must agree: the panel builds the redirect address from it, not from the request. Then start the panel again so it reads the file:

```sh
cd /opt/server-control-panel && docker compose up -d
```

`PANEL_SECRET_KEY` must be set too. The install generates it. If Settings says "PANEL_SECRET_KEY is not set", run the install command again: it appends a key to an older `panel.env` and changes nothing else.

## 6. Connect from Settings

1. Open the panel, then **Settings**.
2. In the **Google Drive** card choose **Connect Google Drive**. Cloudflare Access may ask you to sign in again first.
3. Google asks you to choose the account and to allow access to files the app creates. Allow it. You return to Settings with the account's email shown.
4. Create the destination folder: choose **Create new folder**, keep `server-backups` or type another name, and choose **Create**. You can later switch to another folder the panel created.
5. Choose **Test connection**. The panel uploads a tiny file to the folder and deletes it again, and tells you in words whether it worked.

After this, **Back up now** works on the databases the panel can dump. Without a connected Drive that button first opens "Connect Google Drive first", and after consent it brings you back to the same database.

## When the token expires

Drive access can end for these reasons: you revoked the panel under your Google account's third-party access, the panel's `PANEL_SECRET_KEY` changed or was lost, or the Google Cloud app is still in Testing (7 days, see step 3).

When it happens, the panel shows "The Google Drive token has expired" on Settings and on the Backups page, backups fail until you fix it (a refused backup stays in the history), and nothing already uploaded is lost. To recover:

1. If the cause is Testing, publish the app as in step 3. A token issued in Testing stays short-lived, so reconnect afterwards.
2. In Settings choose **Reconnect Google Drive** and allow access again. The folder you chose is kept when you reconnect with the same Google account.

If you lose or change `PANEL_SECRET_KEY` the saved token cannot be read: the card shows the same expired state, and reconnecting is all it takes. No backup is lost.

## Troubleshooting

| What you see | What to do |
| --- | --- |
| Settings says "Google Drive is not set up on this server" and names a variable | Set the named variable in `panel.env` and run `docker compose up -d` (step 5). |
| Google shows `redirect_uri_mismatch` | The redirect URI in step 4 differs from `<PANEL_PUBLIC_URL>/api/drive/callback`. Fix one of them. |
| Google says the app is blocked or not verified | The app is still in Testing and your account is not a test user, or the scope is wrong. Publish the app (step 3) and check that only `drive.file` is added. |
| "This link has expired. Go back to Settings and connect again." | The connect link is valid for 10 minutes and works once. Start again from Settings. |
| Test connection says the folder is missing | The folder was deleted or is in the Drive trash. Choose or create a folder. |
| Test connection says Google Drive is full | Free up space in the Google account, or use another account. |
