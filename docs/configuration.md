# Configuration

Everything is an environment variable; every one has a default.

## Basics

| Variable | Default | What |
| :--- | :--- | :--- |
| `PORT` | `8080` | The app |
| `PUBLIC_PORT` | `0` (off) | A second listener with only share and upload pages — expose this one |
| `HOST` | `0.0.0.0` | Address to listen on |
| `BASE_URL` | — | Where the app is reached (`https://files.example.com`); needed for sign-in callbacks |
| `PUBLIC_URL` | `BASE_URL` | Where links point (`https://send.example.com`, the host in front of `PUBLIC_PORT`) |
| `DATA_DIR` | `./data` (`/data` in the image) | Database, homes, sends, trash, thumbnails |
| `FILES_DIR` | `DATA_DIR/files` | One folder per person (`<id>`) |
| `SENDS_DIR` | `DATA_DIR/sends` | Uploaded links (ciphertext for encrypted ones) |
| `TRASH_DIR` | `DATA_DIR/trash` | Must be on the same disk as `FILES_DIR` for instant deletes |
| `THUMBS_DIR` | `DATA_DIR/thumbs` | Image thumbnails (safe to delete) |
| `TZ` | `UTC` | Time zone |
| `LOG_LEVEL` / `LOG_FORMAT` | `info` / `text` | `debug`…`error` / `json` |
| `TRUST_PROXY` | `true` | Take the client address from `X-Forwarded-For` / `X-Real-IP` |
| `METRICS_ENABLED` | `true` | Prometheus text at `/metrics` |

## Mounts

| Variable | Default | What |
| :--- | :--- | :--- |
| `MOUNT_ROOTS` | — (off) | Comma-separated folders; admins may mount these or anything below them. Bind them into the container at the same path |

## Links

| Variable | Default | What |
| :--- | :--- | :--- |
| `MAX_SEND_MB` | `10240` | Largest send (all files together); `0` = no limit |
| `MAX_SEND_DAYS` | `0` | Longest expiry in days; `0` also allows "never" |
| `MAX_REQUEST_MB` | `0` | Largest file through an upload link (the link's own limit can be lower) |
| `PUBLIC_CHUNK_MB` | `64` | Piece size for uploads through upload links (Cloudflare caps a request at 100 MB) |

## Sign-in

| Variable | Default | What |
| :--- | :--- | :--- |
| `AUTH_MODE` | `local` | `local` (accounts), `proxy` (trust `AUTH_PROXY_HEADER`), `none` (single user, no login) |
| `ADMIN_EMAIL` / `ADMIN_PASSWORD` / `ADMIN_NAME` | — | Create the first admin on start instead of the setup screen |
| `SECURE_COOKIES` | `false` | Set behind HTTPS |
| `SESSION_TTL` | `720h` | How long a sign-in lasts |
| `OIDC_ISSUER` / `OIDC_CLIENT_ID` / `OIDC_CLIENT_SECRET` / `OIDC_NAME` | — | Any OpenID Connect provider (callback `BASE_URL/api/v1/auth/oidc/callback`) |
| `OIDC_AUTO_CREATE` | `true` | Make an account on first sign-in |

## Pocket Core

| Variable | What |
| :--- | :--- |
| `CORE_URL` | Core's address; the app registers on start, gets a sign-in client, publishes events and accepts phone and link tokens |
| `CORE_REGISTRATION_KEY` | Core's registration key |
| `CORE_APP_URL` | Where Core reaches this app (default `BASE_URL`) |

Without `CORE_URL` nothing of Core is used.

## Notifications

| Variable | What |
| :--- | :--- |
| `NOTIFY_URL` / `NOTIFY_KIND` | The owner's default channel (`apprise`, `ntfy`, `webhook`); everyone can set their own in Settings |

## Email

Everyone sets their own mail account in Settings → Email. These seed the owner's once:

| Variable | Default | What |
| :--- | :--- | :--- |
| `SMTP_HOST` / `SMTP_PORT` / `SMTP_TLS` | — / `587` / `starttls` | `starttls`, `tls` (465) or `none` |
| `SMTP_USER` / `SMTP_PASSWORD` | — | Login (Gmail: an app password) |
| `SMTP_FROM` / `SMTP_FROM_NAME` | — | Sender |

## Command line

```
pocket-files                      start the server
pocket-files backup [-o FILE]     consistent SQLite copy
pocket-files reset-password [-email USER] [-password NEW]
pocket-files version
```

Back up `DATA_DIR` (or at least the database, `files/` and `sends/`); `thumbs/` can be skipped.
