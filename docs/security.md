# Security design

## Encrypted links

Encrypted sends follow SkySend and Firefox Send: the server stores ciphertext it cannot read.

| Piece | How |
| :--- | :--- |
| Secret | 256 random bits, made in the browser, carried only in the link's `#fragment` (browsers never send fragments to servers) |
| Password (optional) | PBKDF2-SHA256, 600 000 rounds, random 128-bit salt (stored with the send); the 256-bit result is appended to the secret |
| Keys | HKDF-SHA256 over secret (‖ password key), salt `pocket-files/v1`, info `content`, `meta` and `auth` |
| File contents | AES-256-GCM in records of 64 KiB. Each file has a random 96-bit IV; record *n* uses the IV with its last 32 bits XORed with *n*. The additional data is one byte: 1 on the last record, else 0 — records cannot be reordered, dropped or cut off unnoticed |
| Metadata | File names, sizes, types, per-file IVs, the note's format: JSON sealed with AES-256-GCM (random IV) under the `meta` key |
| Access | The `auth` bits (base64url) are the auth token. The server keeps only its SHA-256 and hands out a download ticket to whoever shows the token |

Consequences:

- Without the whole link the server will not even hand out the ciphertext; with the link but
  without the password, the keys come out wrong and the server refuses too (wrong auth token).
- The server cannot check passwords of encrypted links and cannot rate-limit guesses offline — it
  limits online attempts instead: ten wrong tries per link and address, fifty per link, per 15
  minutes.
- The owner's list of links shows the link with its key only in the browser that made it
  (localStorage). The server never has the key, so it cannot show it anywhere else.
- Decryption happens in the recipient's browser, into memory; very large encrypted sends need the
  memory (or the disk space the browser pages blobs to). `MAX_SEND_MB` caps sends (default 10 GB).

## Live links

A live link points at files where they are. It is not encrypted (TLS protects the transfer). A
password is checked by the server with bcrypt and the same attempt limits.

## Download counting

Opening a link counts one download and returns a *ticket* valid for six hours that unlocks every
file of that send (ranges included), so a big download can resume and multi-file sends count once.
When the limit is reached no new tickets are given; the stored files are removed once the last
ticket expires.

## The public listener

`PUBLIC_PORT` starts a second HTTP server whose router contains only `/s/*`, `/r/*`, the static
assets and `/api/v1/public/*` (rate limited per address). Sign-in, the file API, WebDAV and settings
do not exist there. Expose that port, keep `PORT` private.

## Files on the server

- Every path is cleaned and resolved against its root; every folder on the way, symlinks included,
  must stay inside the home, mount or share. Symlinks pointing outside are invisible and refused.
- Mounts can only be made below `MOUNT_ROOTS`, are re-checked on every use, and only admins make them.
- Downloads and previews carry `Content-Security-Policy: sandbox` (PDF excepted) and
  `X-Content-Type-Options: nosniff`; HTML, XML and scripts preview as plain text. A shared file
  never runs script in the app's origin. WebDAV never accepts the browser's session cookie.
- Strictly per person: homes, shares, sends, upload links, stars and the trash are scoped to the
  signed-in person; ids of anyone else's answer 404, for admins as well.
