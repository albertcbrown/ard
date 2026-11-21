-- Widen two CHECK constraints (SQLite cannot alter them in place): findings
-- gain the 'gone' status, and runs gain the outcomes the scanner pipeline
-- records. 'dropped' stays valid for rows written before this migration.
CREATE TABLE findings_new(
  fingerprint TEXT PRIMARY KEY,
  issue INTEGER NOT NULL,
  status TEXT NOT NULL CHECK(status IN ('open','gone','fixed')),
  severity TEXT NOT NULL, group_key TEXT NOT NULL,
  first_seen INTEGER NOT NULL, last_seen INTEGER NOT NULL, last_sha TEXT);
INSERT INTO findings_new SELECT fingerprint, issue, status, severity, group_key, first_seen, last_seen, last_sha FROM findings;
DROP TABLE findings;
ALTER TABLE findings_new RENAME TO findings;
CREATE INDEX findings_issue ON findings(issue);
CREATE TABLE runs_new(
  id INTEGER PRIMARY KEY, started_at INTEGER NOT NULL, finished_at INTEGER,
  sha TEXT,
  outcome TEXT NOT NULL CHECK(outcome IN ('ok','dropped','dropped_backpressure','dropped_global_cap','scan_failed','timeout','error')),
  found INTEGER NOT NULL DEFAULT 0, filed INTEGER NOT NULL DEFAULT 0, closed INTEGER NOT NULL DEFAULT 0);
INSERT INTO runs_new SELECT id, started_at, finished_at, sha, outcome, found, filed, closed FROM runs;
DROP TABLE runs;
ALTER TABLE runs_new RENAME TO runs;
