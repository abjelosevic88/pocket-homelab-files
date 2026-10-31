# Patched rclone for iCloud sign-in only

`rclone-icloud-auth` in the image is rclone v1.75.2 plus
[rclone#9982](https://github.com/rclone/rclone/pull/9982) ("iclouddrive: fix 2FA failing with 409 on
non-ADP accounts"), applied from `9982-iclouddrive-2fa-409.patch` (checksum in `SHA256SUMS`, checked
during the build). Since mid-2026 Apple answers a correct 2FA code with HTTP 409 and
`"securityCode": {"valid": true}`; released rclone treats that as a failure for accounts without
Advanced Data Protection.

It is used **only** for the iCloud Drive sign-in steps (`rclone config create/update` for an
`iclouddrive` remote). Every sync, listing and copy runs the official rclone. Reviewed 2026-10-31:
the patch changes how the securitycode/trust responses are read and sends icloud.com-style
Origin/Referer on those calls; no other endpoints, no file access.

Remove this (the build stage, `RCLONE_ICLOUD_AUTH`, and this folder) once an rclone release contains
the fix.
