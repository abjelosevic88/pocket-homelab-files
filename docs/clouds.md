# Connected drives

Each person can connect their own Google Drive, Dropbox, OneDrive, iCloud Drive, or any other
[rclone](https://rclone.org) remote (S3, SFTP, WebDAV, pCloud, Mega, Box, …) in **Settings → Connected
drives**. Nobody else sees them, admins included.

A drive is copied **one way** into Pocket Files with rclone `copy`: nothing in the cloud is ever written,
and a file deleted in the cloud stays here. Two ways to copy:

| | Where the copy lives | |
| --- | --- | --- |
| **Keep in sync** (every hour, 6 h, 12 h, day or week) | its own read-only place under **Drives** in the sidebar (`CLOUD_DIR/<user>/<drive>`) | new and changed files arrive on the schedule; "Sync now" any time |
| **Copy once** | a new folder in **My files** | ordinary files from then on; "Copy again" brings in new and changed ones |

**Where** can also be a folder you pick: anywhere in My files or in a mount you can edit (say
`Media/photos`). Files then go straight into that folder, scheduled or once, and the drive gets no place
of its own. Because you may work in that folder too, these copies run with `--update`: a file you changed
after the cloud did is never replaced. Disconnecting leaves the folder as it is. The destination is fixed
when connecting; to move it, connect the drive again.

Disconnecting a scheduled drive deletes its copy, or moves it into My files when "Keep the copied files"
is on. One-time copies already live in My files and stay.

Google Docs, Sheets and Slides arrive as Word, Excel and PowerPoint files (they have no file of their own
in Drive). "Also Shared with me" adds a `Shared with me` folder. "Only this folder" copies one folder of
the cloud.

## Sign-in

| Kind | How | Box setting |
| --- | --- | --- |
| Google Drive | OAuth, scope `drive.readonly` | `GOOGLE_CLIENT_ID`, `GOOGLE_CLIENT_SECRET` |
| Dropbox | OAuth, `files.metadata.read files.content.read account_info.read` | `DROPBOX_APP_KEY`, `DROPBOX_APP_SECRET` |
| OneDrive | OAuth, `Files.Read Files.Read.All User.Read offline_access` | `MICROSOFT_CLIENT_ID`, `MICROSOFT_CLIENT_SECRET`, `MICROSOFT_TENANT` (default `common`) |
| iCloud Drive | Apple ID + password, then the code Apple sends to your devices | none |
| Other | paste one remote from `rclone config show` | none |

Admins set the three sign-in apps up in **Settings → Connected drives → Sign-in apps** (or from the
greyed-out drive in "Connect a drive"): a step-by-step guide per provider with the redirect address to
copy, fields for the client ID and secret, and **Check & save**, which asks the provider whether it knows
that client before anything is saved (Google and Dropbox: a made-up authorization code is refused as
`invalid_grant` for a real client and `invalid_client` for a wrong one; Microsoft: an app-only token
request on the `consumers` endpoint, which names an unknown app or a wrong secret). Saved values are
box settings (`cloud_app.<kind>.id/secret/tenant`, the secret sealed) and win over the environment
variables below; Remove falls back to the environment. `GET/PUT/DELETE /api/v1/clouds/apps[/{kind}]`.

OAuth redirect URI to register with each provider: `BASE_URL/api/v1/clouds/oauth/<kind>/callback`
(`gdrive`, `dropbox`, `onedrive`). Pocket Photos uses the same variable names and path, so one client
per provider can serve both apps: register both redirect URIs.

- **Google**: Google Cloud console → APIs & Services → enable the Google Drive API → Credentials → OAuth
  client ID → *Web application*. Publish the app (Google Auth Platform → Audience → Publish app);
  in *Testing* the refresh token dies after 7 days.
- **Dropbox**: dropbox.com/developers → Create app → Scoped access, Full Dropbox → Permissions: the
  three scopes above → Settings: add the redirect URI.
- **Microsoft**: Azure portal → App registrations → New (accounts in any organizational directory and
  personal Microsoft accounts) → Redirect URI (Web) → Certificates & secrets → new client secret.

**iCloud**: Advanced Data Protection must be off and "Access iCloud Data on the Web" on. Apple ends the
session about once a month; the drive then shows "Sign in again" (password + a new code).

Sign-ins (tokens, cookies, pasted remotes) are sealed with AES-256-GCM under `DATA_DIR/secret.key`. The
box's OAuth client secrets are never stored with them. Refreshed tokens are written back after each run.

## Running

`CLOUD_SYNC_CONCURRENCY` drives copy at once (default 2); others wait. Each run uses `--transfers 4
--checkers 8`, Google `--tpslimit 10 --fast-list`. A drive whose sign-in is refused stops at "Sign in
again" and its owner gets one notification (Settings → Notifications); other failures retry on the next
schedule. A run that was going when the app stopped starts again on schedule.

Copies are staging, like the cloud itself: `CLOUD_DIR` is not part of the app's backups. One-time copies
land in My files and are backed up with it.

## API

All routes need a signed-in person and only see that person's drives; other ids answer 404.

| Route | |
| --- | --- |
| `GET /api/v1/clouds/providers` | `{providers: [{kind, name, auth, configured, fields?, help}], rclone, rclone_error?, redirect}` |
| `GET /api/v1/clouds` | `[Cloud]` (with live `progress` while copying, `question` while iCloud waits for a code) |
| `POST /api/v1/clouds` | form kinds `{kind: "icloud"\|"rclone", name, interval_hours, source?, dest_space?, dest_path?, fields}` → `Cloud` |
| `GET /api/v1/clouds/oauth/{kind}/start` | `?name=&interval_hours=&shared_with_me=&source=&dest_space=&dest_path=&cloud=ID` → `{url}`; `cloud` reconnects |
| `GET /api/v1/clouds/oauth/{kind}/callback` | provider redirect → `/settings/clouds?connected=ID` or `?error=` |
| `POST /api/v1/clouds/{id}/code` | `{code}` → `Cloud` |
| `POST /api/v1/clouds/{id}/reconnect` | iCloud `{password}` → `Cloud` (asks for a new code) |
| `PUT /api/v1/clouds/{id}` | `{name?, interval_hours?, shared_with_me?, source?}` |
| `POST /api/v1/clouds/{id}/sync`, `/stop` | start now / stop the running copy |
| `DELETE /api/v1/clouds/{id}[?keep=1]` | disconnect → `{ok, folder?}` |

`interval_hours` 0 = copy once. `dest_space` is `home` or `mount:N` (writable for the person). A drive
stays scheduled or one-time, and keeps its destination; only the schedule can change.
