-- Who opened a link, from where, and what they actually took.
ALTER TABLE send_log ADD COLUMN country TEXT NOT NULL DEFAULT '';  -- ISO code from Cloudflare (CF-IPCountry)
ALTER TABLE send_log ADD COLUMN region  TEXT NOT NULL DEFAULT '';  -- only with Cloudflare's visitor location headers
ALTER TABLE send_log ADD COLUMN city    TEXT NOT NULL DEFAULT '';
ALTER TABLE send_log ADD COLUMN lang    TEXT NOT NULL DEFAULT '';  -- first Accept-Language tag
ALTER TABLE send_log ADD COLUMN tz      TEXT NOT NULL DEFAULT '';  -- the visitor's time zone, as their browser reports it
ALTER TABLE send_log ADD COLUMN referer TEXT NOT NULL DEFAULT '';  -- the page the link was clicked on, when the browser tells
ALTER TABLE send_log ADD COLUMN files   TEXT NOT NULL DEFAULT '[]'; -- JSON list of files fetched with this open
ALTER TABLE send_log ADD COLUMN bytes   INTEGER NOT NULL DEFAULT 0; -- bytes served with this open
ALTER TABLE send_log ADD COLUMN last_at TEXT NOT NULL DEFAULT '';  -- last fetch with this open
ALTER TABLE send_tickets ADD COLUMN log_id INTEGER NOT NULL DEFAULT 0;
CREATE INDEX IF NOT EXISTS send_log_send ON send_log (send_id, id);
