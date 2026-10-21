# WebDAV, and Pocket apps keeping their files here

## For people

| What | Value |
| :--- | :--- |
| Address | `https://files.example.com/dav/` |
| Nextcloud-shaped address | `https://files.example.com/remote.php/dav/files/<user name>/` (also `/remote.php/webdav/`) |
| User name | anything (your WebDAV user name is shown in Settings) |
| Password | an app password (Settings → WebDAV & apps), or your account password |

The root is your home. Two made-up folders sit next to your own:

- **Shared with me** — one folder per share someone gave you (a shared file appears as a file).
- **Mounts** — one folder per server folder an admin mounted for you.

Those two names are reserved at the top of your home. Deleting through WebDAV moves things to the
trash (your own, or the owner's for a share); in a mount it deletes for good. WebDAV never accepts the
browser's session cookie, only explicit credentials.

## For other Pocket apps (through Pocket Core)

Pocket Files registers with Core as `files` and offers two link scopes:

| Scope | Allows |
| :--- | :--- |
| `files:read` | WebDAV `GET HEAD OPTIONS PROPFIND`; API `GET /api/v1/spaces`, `/stars`, `/fs/list`, `/fs/stat`, `/fs/raw`, `/fs/zip`, `/fs/search`, `/fs/thumb`, `/fs/usage` |
| `files:write` | WebDAV `PUT DELETE MKCOL MOVE COPY PROPPATCH LOCK UNLOCK`; API `PUT /fs/upload`, `POST /fs/mkdir|move|copy|rename|delete|text` |

Sends, upload links, shares, mounts, settings and tokens stay closed to links.

An app that wants to keep a person's files here:

1. declares `uses: [{app: "files", scopes: ["files:read", "files:write"], reason: "…"}]` in its manifest;
2. asks Core for a short-lived token for that person: SDK `ActAs(ctx, email, "files", "files:read", "files:write")`;
3. resolves the address with `Resolve(ctx, "files")` (use `url` from inside the box, `public_url` for browsers);
4. talks WebDAV to `<url>/dav/` with Basic auth — any user name, the `pcx_` token as the password —
   or sends `Authorization: Bearer pcx_…`.

Ask for a new token when one expires (they live five minutes); Core re-checks the person's and the
admin's switches every time, so turning the link off in Core → Links takes effect at once.

ETags follow the file's size and modification time and change on every write. File ids are paths:
a file moved inside Pocket Files is a new file to other apps.
