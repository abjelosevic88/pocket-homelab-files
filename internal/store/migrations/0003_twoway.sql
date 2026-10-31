-- Two-way drives: native Google Docs this side last saw, and files changed on both sides.
CREATE TABLE cloud_docs (
  cloud_id     INTEGER NOT NULL REFERENCES clouds(id) ON DELETE CASCADE,
  path         TEXT NOT NULL,              -- relative to the drive's folder ("Work/Plan.docx")
  file_id      TEXT NOT NULL,
  format       TEXT NOT NULL,              -- docx | xlsx | pptx
  remote_mtime TEXT NOT NULL,              -- RFC 3339, as Google last reported it
  local_mtime  INTEGER NOT NULL,           -- unix ns of the local copy after the last sync
  local_size   INTEGER NOT NULL,
  PRIMARY KEY (cloud_id, path)
);
CREATE TABLE cloud_conflicts (
  id          INTEGER PRIMARY KEY,
  cloud_id    INTEGER NOT NULL REFERENCES clouds(id) ON DELETE CASCADE,
  path        TEXT NOT NULL,               -- the original name, relative to the drive's folder
  cloud_name  TEXT NOT NULL,               -- where the cloud's version is now
  pocket_name TEXT NOT NULL,               -- where this side's version is now
  doc         INTEGER NOT NULL DEFAULT 0,  -- a native Google Doc (cloud_name IS the Doc)
  created_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
  UNIQUE (cloud_id, path)
);
