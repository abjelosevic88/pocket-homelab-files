# API

JSON over HTTP under `/api/v1`. Authenticate with the session cookie (browser), `Authorization:
Bearer <app password>` (`pf_…`), Basic auth with an app password, or a Pocket Core token (`pcd_`,
`pcu_`, `pco_`; `pcx_` link tokens within their scope, see [webdav.md](webdav.md)).

Places are *spaces*: `home` (your files), `mount:<id>`, `share:<id>`. Paths inside a space look like
`/Documents/report.pdf`.

## Files

| Call | What |
| :--- | :--- |
| `GET /spaces` | Places you can open: home, mounts, shares with you (`perm` view/edit) |
| `GET /fs/list?space=&path=&hidden=` | `{space, path, entry, entries[], writable}`; entries carry `starred` and (home) `shared` |
| `GET /fs/stat?space=&path=` | One entry |
| `GET /fs/raw?space=&path=&dl=1` | The file (ranges; `dl=1` as attachment) |
| `GET /fs/zip?space=&path=…&path=…&name=` | Zip of files and folders, streamed |
| `GET /fs/thumb?space=&path=&size=` | JPEG thumbnail of a JPEG/PNG/GIF/WebP |
| `PUT /fs/upload?space=&path=<folder>&name=<name or sub/path>&conflict=rename\|overwrite\|skip\|fail&mtime=<ms>` | Body = the file |
| `POST /fs/mkdir` `{space, path}` | New folder (parents too) |
| `POST /fs/rename` `{space, path, name}` | |
| `POST /fs/move` / `POST /fs/copy` `{space, paths[], to_space, to_path, conflict}` | Per-item results `[{from, to, error}]` |
| `POST /fs/delete` `{space, paths[], permanent}` | Homes and shares → trash; mounts → gone |
| `POST /fs/text` `{space, path, content}` | Save a text file |
| `GET /fs/search?space=&path=&q=` | Names containing all words, below path (time-boxed, 300 max) |
| `GET /fs/usage?space=&path=` | `{files, size}` below path |
| `GET /stars` · `PUT /stars` `{space, path, on}` | Pinned items |
| `GET /trash` · `POST /trash/{id}/restore` · `DELETE /trash/{id}` · `DELETE /trash` | Your trash |

## Sharing

| Call | What |
| :--- | :--- |
| `GET /people` | Everyone on the box (id, name, email) |
| `GET /shares` | `{by_me[], with_me[]}` |
| `GET /shares/path?path=` | Your share of a home path (empty when none) |
| `PUT /shares/path` `{path, members:[{user_id, perm}], everyone_perm, note}` | Set it; no members and no everyone removes it |
| `DELETE /shares/{id}` | Stop sharing |
| `GET /mounts` · `POST /mounts` · `PUT /mounts/{id}` · `DELETE /mounts/{id}` | Admins: `{name, path, read_only, everyone_perm, access:[{user_id, perm}]}` |
| `GET /mounts/browse?path=` | Admins: folders below `MOUNT_ROOTS` |

## Links (sends)

| Call | What |
| :--- | :--- |
| `GET /sends` · `GET /sends/{id}` | Yours, with `url`, `available`, `reason`; one with its download `log` |
| `POST /sends` | `{kind: files\|note\|live, encrypted, label, expires: 5m\|1h\|1d\|7d\|30d\|never, max_downloads, notify, password}`; files/note: `files:[{expected, name, type}]` (+ `meta`, `auth_hash`, `password_salt` when encrypted); live: `live:{space, paths[]}` |
| `PUT /sends/{id}/files/{idx}?offset=` | Append a piece (a retried piece truncates back to its offset) |
| `POST /sends/{id}/finish` | Ready when every file has its announced size |
| `PUT /sends/{id}` `{label, expires (or "now"), max_downloads, notify}` | Change limits |
| `DELETE /sends/{id}` | Kill it |
| `POST /sends/{id}/email` `{to: ["a@x, b@y"], message, link}` | Email it from your mail account; encrypted links need `link` (the whole link with its key, used once, never stored) |

Public (no sign-in; also on `PUBLIC_PORT`): `GET /public/sends/{id}`, `POST /public/sends/{id}/open`
`{auth}` or `{password}` → `{ticket, …}`, `GET /public/sends/{id}/files/{idx}?t=`, live links
`GET /public/sends/{id}/live/list|file|zip?t=&path=`.

## Upload links (requests)

| Call | What |
| :--- | :--- |
| `GET /requests` · `POST /requests` · `PUT /requests/{id}` · `DELETE /requests/{id}` | `{title, message, space, path, expires, max_files, max_file_mb, password, notify, closed}` |
| `POST /requests/{id}/email` `{to, message}` | Email the upload link |

Public: `GET /public/requests/{id}` (→ `token` unless a password is set), `POST …/unlock {password}`,
`POST …/uploads {token, name, size, from}` → `{id}`, `PUT …/uploads/{uid}?offset=&token=` (pieces of up
to `PUBLIC_CHUNK_MB`), `POST …/uploads/{uid}/finish {token}`.

## Account and box

`GET /auth/status`, `POST /auth/login`, `POST /auth/setup`, `POST /auth/logout`, `GET|PUT /auth/me`,
`GET|POST /tokens`, `DELETE /tokens/{id}`, `GET|PUT /settings`, `GET /settings/system`,
`POST /notify/test`, `POST /email/test {to}`, `GET|POST /users`, `PUT|DELETE /users/{id}` (admins), `GET /backup` (admins,
single-person boxes), `GET /changes` (a stamp that moves on every change).

`GET /healthz`, `GET /readyz`, `GET /metrics`.
