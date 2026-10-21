CREATE TABLE users (
  id            INTEGER PRIMARY KEY,
  email         TEXT NOT NULL UNIQUE,
  name          TEXT NOT NULL DEFAULT '',
  username      TEXT NOT NULL DEFAULT '',
  password_hash TEXT NOT NULL DEFAULT '',
  role          TEXT NOT NULL DEFAULT 'admin',
  feed_token    TEXT NOT NULL DEFAULT '',
  created_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
  updated_at    TEXT
);
CREATE UNIQUE INDEX users_username ON users(username) WHERE username <> '';

CREATE TABLE sessions (
  id         TEXT PRIMARY KEY,
  user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  expires_at TEXT NOT NULL,
  created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

CREATE TABLE api_tokens (
  id           INTEGER PRIMARY KEY,
  user_id      INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name         TEXT NOT NULL DEFAULT '',
  token_hash   TEXT NOT NULL UNIQUE,
  prefix       TEXT NOT NULL DEFAULT '',
  last_used_at TEXT,
  created_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

-- Box-wide settings (title, setup_complete). Everything a person chooses
-- is in user_settings under their id.
CREATE TABLE settings (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL DEFAULT ''
);
CREATE TABLE user_settings (
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  key     TEXT NOT NULL,
  value   TEXT NOT NULL DEFAULT '',
  PRIMARY KEY (user_id, key)
);

-- A mount is a folder on the server an admin made visible in the app.
-- Who sees it: everyone (everyone_perm) and/or the people in mount_access.
CREATE TABLE mounts (
  id            INTEGER PRIMARY KEY,
  name          TEXT NOT NULL UNIQUE,
  path          TEXT NOT NULL,
  read_only     INTEGER NOT NULL DEFAULT 0,
  everyone_perm TEXT NOT NULL DEFAULT '',   -- '' | view | edit
  color         TEXT NOT NULL DEFAULT '',
  created_by    INTEGER REFERENCES users(id) ON DELETE SET NULL,
  created_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
CREATE TABLE mount_access (
  mount_id INTEGER NOT NULL REFERENCES mounts(id) ON DELETE CASCADE,
  user_id  INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  perm     TEXT NOT NULL DEFAULT 'view',    -- view | edit
  PRIMARY KEY (mount_id, user_id)
);

-- A share hands one folder (or file) of someone's own files to other people
-- of the box. The path is relative to the owner's home.
CREATE TABLE shares (
  id            INTEGER PRIMARY KEY,
  owner_id      INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  path          TEXT NOT NULL,
  is_dir        INTEGER NOT NULL DEFAULT 1,
  everyone_perm TEXT NOT NULL DEFAULT '',
  note          TEXT NOT NULL DEFAULT '',
  created_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
  UNIQUE (owner_id, path)
);
CREATE TABLE share_members (
  share_id INTEGER NOT NULL REFERENCES shares(id) ON DELETE CASCADE,
  user_id  INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  perm     TEXT NOT NULL DEFAULT 'view',
  PRIMARY KEY (share_id, user_id)
);
CREATE INDEX share_members_user ON share_members(user_id);

-- A send is a link for anyone: uploaded files (usually encrypted in the
-- browser, the key only in the link's #fragment), a note, or a live link to
-- files that stay where they are.
CREATE TABLE sends (
  id             TEXT PRIMARY KEY,
  owner_id       INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  kind           TEXT NOT NULL DEFAULT 'files',   -- files | note | live
  encrypted      INTEGER NOT NULL DEFAULT 0,
  label          TEXT NOT NULL DEFAULT '',        -- the owner's name for it (plain)
  meta           TEXT NOT NULL DEFAULT '',        -- encrypted: base64 ciphertext; else JSON
  live_space     TEXT NOT NULL DEFAULT '',
  live_paths     TEXT NOT NULL DEFAULT '[]',      -- JSON list of paths inside live_space
  file_count     INTEGER NOT NULL DEFAULT 0,
  total_size     INTEGER NOT NULL DEFAULT 0,
  has_password   INTEGER NOT NULL DEFAULT 0,
  password_hash  TEXT NOT NULL DEFAULT '',        -- plain sends: bcrypt
  password_salt  TEXT NOT NULL DEFAULT '',        -- encrypted sends: PBKDF2 salt
  auth_hash      TEXT NOT NULL DEFAULT '',        -- encrypted sends: sha256 of the auth token
  expires_at     TEXT NOT NULL DEFAULT '',        -- '' = never
  max_downloads  INTEGER NOT NULL DEFAULT 0,      -- 0 = unlimited
  downloads      INTEGER NOT NULL DEFAULT 0,
  notify         INTEGER NOT NULL DEFAULT 0,
  status         TEXT NOT NULL DEFAULT 'uploading', -- uploading | ready
  last_download_at TEXT NOT NULL DEFAULT '',
  created_at     TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
CREATE INDEX sends_owner ON sends(owner_id);
CREATE TABLE send_files (
  send_id    TEXT NOT NULL REFERENCES sends(id) ON DELETE CASCADE,
  idx        INTEGER NOT NULL,
  size       INTEGER NOT NULL DEFAULT 0,      -- bytes stored (ciphertext when encrypted)
  expected   INTEGER NOT NULL DEFAULT 0,      -- bytes announced at create
  name       TEXT NOT NULL DEFAULT '',        -- plain sends only
  type       TEXT NOT NULL DEFAULT '',
  PRIMARY KEY (send_id, idx)
);
-- A ticket is one counted download: it unlocks every file of the send for a while.
CREATE TABLE send_tickets (
  token_hash TEXT PRIMARY KEY,
  send_id    TEXT NOT NULL REFERENCES sends(id) ON DELETE CASCADE,
  expires_at TEXT NOT NULL
);
CREATE TABLE send_log (
  id      INTEGER PRIMARY KEY,
  send_id TEXT NOT NULL REFERENCES sends(id) ON DELETE CASCADE,
  at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
  ip      TEXT NOT NULL DEFAULT '',
  agent   TEXT NOT NULL DEFAULT ''
);

-- A request is an upload link: whoever has it can drop files into one folder.
CREATE TABLE requests (
  id             TEXT PRIMARY KEY,
  owner_id       INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  title          TEXT NOT NULL DEFAULT '',
  message        TEXT NOT NULL DEFAULT '',
  space          TEXT NOT NULL,
  path           TEXT NOT NULL,
  expires_at     TEXT NOT NULL DEFAULT '',
  max_files      INTEGER NOT NULL DEFAULT 0,
  max_file_mb    INTEGER NOT NULL DEFAULT 0,
  password_hash  TEXT NOT NULL DEFAULT '',
  notify         INTEGER NOT NULL DEFAULT 1,
  closed         INTEGER NOT NULL DEFAULT 0,
  files_received INTEGER NOT NULL DEFAULT 0,
  bytes_received INTEGER NOT NULL DEFAULT 0,
  last_upload_at TEXT NOT NULL DEFAULT '',
  created_at     TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
CREATE INDEX requests_owner ON requests(owner_id);

-- Uploads in progress, sent in pieces (request links go through Cloudflare,
-- which caps one request at 100 MB).
CREATE TABLE uploads (
  id         TEXT PRIMARY KEY,
  owner      TEXT NOT NULL,                  -- request:<id> | send:<id>
  name       TEXT NOT NULL DEFAULT '',
  size       INTEGER NOT NULL DEFAULT 0,
  received   INTEGER NOT NULL DEFAULT 0,
  temp       TEXT NOT NULL,
  extra      TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

CREATE TABLE trash (
  id         INTEGER PRIMARY KEY,
  user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name       TEXT NOT NULL,
  orig_path  TEXT NOT NULL,                  -- inside the owner's home
  stored     TEXT NOT NULL,                  -- name under TRASH_DIR/<user>/
  is_dir     INTEGER NOT NULL DEFAULT 0,
  size       INTEGER NOT NULL DEFAULT 0,
  deleted_by INTEGER,
  deleted_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
CREATE INDEX trash_user ON trash(user_id);

CREATE TABLE stars (
  user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  space      TEXT NOT NULL,
  path       TEXT NOT NULL,
  is_dir     INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
  PRIMARY KEY (user_id, space, path)
);
