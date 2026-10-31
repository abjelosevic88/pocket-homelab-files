# Search

Pocket Files keeps one full-text index of everything it serves: every person's home, every mount and
every scheduled drive copy. It is SQLite FTS5 in its own file, `DATA_DIR/search.db`, next to the app's
database: no Elasticsearch, no extra service. Delete the file (or Settings → About → Rebuild) and it
builds again.

## What is found

- **Names**, anywhere in a file or folder name or its folder path: an FTS5 *trigram* index, so `voice ovh`
  finds `Invoices/2026-Invoice-OVH.pdf`. Words shorter than three letters match with `LIKE`.
- **Text inside documents**: txt, md, csv, json, yaml, xml, html, code, PDF (`pdftotext`, first 300 pages),
  Word / Excel / PowerPoint (docx, xlsx, pptx), OpenDocument (odt, ods, odp) and EPUB. Up to 256 KB of
  text per document. Ranked with bm25; results carry a snippet with the matched words marked
  (`…`). Words match by prefix (`invo` finds *invoice*).
- Accents do not matter: `cevapi` finds *Ćevapi*; `đ` is indexed as `dj` (`djuvec` finds *Đuveč*).
- Hidden files and folders (starting with a dot) are left out.

Mounts are indexed by **name only** unless `SEARCH_MOUNT_TEXT=true`: they are usually media and book
libraries whose text would make the index huge.

Search box syntax: words (all must match), `"exact phrase"`, `-without`, `ext:pdf,docx`, `type:file`,
`type:folder`.

## Keeping current

Every change through the app, WebDAV, upload links and drive copies re-reads the folders it touched within
a couple of seconds. Homes and drive copies are also walked every 10 minutes and mounts every hour, which
catches changes made directly on disk. A walk only reads entries whose size or time changed; document
text is read in the background.

## Privacy

Results only ever come from places the person can open: their home, mounts they have access to, their
own connected drives, and folders shared with them (under that share's name and paths).

## API

| Route | |
| --- | --- |
| `GET /api/v1/search?q=&limit=` | everywhere → `{hits: [{space, path, name, folder, is_dir, size, modified, ext, match: "name"\|"content", snippet?}], took_ms}` |
| `GET /api/v1/fs/search?space=&path=&q=` | below one folder → `{entries: [Entry + match, snippet], partial}` |
| `GET /api/v1/search/status` | `{available, status: {files, folders, text_read, text_pending, index_bytes}}` |
| `POST /api/v1/search/rebuild` | admins: drop and rebuild the index |
