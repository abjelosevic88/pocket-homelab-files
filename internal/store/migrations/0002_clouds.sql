-- Connected drives: other clouds copied one way into Pocket Files.
CREATE TABLE clouds (
  id             INTEGER PRIMARY KEY,
  user_id        INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  kind           TEXT NOT NULL,                 -- gdrive | dropbox | onedrive | icloud | rclone
  name           TEXT NOT NULL,
  login          TEXT NOT NULL DEFAULT '',      -- the cloud account's email or user, never a secret
  config         TEXT NOT NULL DEFAULT '',      -- sealed JSON of the rclone remote's options
  options        TEXT NOT NULL DEFAULT '{}',    -- plain JSON: {"shared_with_me": true, ...}
  interval_hours INTEGER NOT NULL DEFAULT 6,    -- 0 = only when asked
  status         TEXT NOT NULL DEFAULT 'idle',  -- idle | syncing | error | reconnect | code
  error          TEXT NOT NULL DEFAULT '',
  last_sync_at   TEXT,
  last_ok_at     TEXT,
  files          INTEGER NOT NULL DEFAULT 0,
  bytes          INTEGER NOT NULL DEFAULT 0,
  created_at     TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
CREATE INDEX clouds_user ON clouds(user_id);
