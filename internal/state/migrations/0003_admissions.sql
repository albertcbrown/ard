CREATE TABLE admissions(
  issue INTEGER PRIMARY KEY,
  mode TEXT NOT NULL,
  reason TEXT NOT NULL,
  labeled_by TEXT,
  labeled_at INTEGER NOT NULL,
  body_hash TEXT NOT NULL,
  admitted_at INTEGER NOT NULL,
  job_id TEXT,
  regated_at INTEGER,
  regated_hash TEXT
);
