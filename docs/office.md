# Edit in Pocket Office

Word, Excel, PowerPoint and OpenDocument files (docx, xlsx, pptx, odt, ods, odp, doc, xls, ppt, rtf,
csv) open in [Pocket Office](https://github.com/abjelosevic88/pocket-homelab-office)'s editor over the
whole window, and saves go straight back to the file. "Preview" stays in the file menu.

It is offered **only when Pocket Office is installed and connected**, for each person, all through
Pocket Core:

1. Pocket Files' link to Pocket Office (`office:open`, "Offer Open in Pocket Office") is on, which also
   means Office is registered with Core;
2. Pocket Office's link to Pocket Files (`files:read`, `files:write`) is on: Pocket Files asks Office
   with the `office:open` token, and Office checks its own link.

New links start switched off in Pocket Core → Links. Until both are on, nothing about Office shows;
`GET /api/v1/office` says why. The answer is remembered for a minute (15 seconds while it is "no").

Office reaches the file over this app's WebDAV drive as the person: My files at the root, mounts under
`Mounts/<name>`, shares under `Shared with me/<name>`. Copies of connected drives are read-only and
not offered. The first time, Office makes the person's "Pocket Files" place by itself.

| Route | |
| --- | --- |
| `GET /api/v1/office` | `{available, reason?}` |
| `GET /api/v1/office/open?space=&path=` | `{url}`: Office's `/open/files?path=<WebDAV path>&embed=1`, loaded in a frame; 409 with the reason when Office is not available |

The frame closes when Office posts `{type: "pocket-office:close"}` (its Close button and the editor's
own close). If a browser will not show Office inside the page, the corner button opens it in a tab.
