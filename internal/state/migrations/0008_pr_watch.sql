CREATE TABLE pr_watch(
  pr INTEGER PRIMARY KEY,
  issue INTEGER NOT NULL,
  head TEXT NOT NULL,
  checks_state TEXT NOT NULL DEFAULT '',
  head_seen_at INTEGER NOT NULL,
  pending_notified_head TEXT NOT NULL DEFAULT '',
  red_notified_head TEXT NOT NULL DEFAULT '',
  closed_notified INTEGER NOT NULL DEFAULT 0,
  merge_recorded INTEGER NOT NULL DEFAULT 0
);
