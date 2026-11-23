CREATE TABLE step_usage(
  job_id TEXT NOT NULL REFERENCES jobs(id) ON DELETE CASCADE,
  step TEXT NOT NULL,
  models TEXT,
  tokens_in INTEGER, tokens_out INTEGER, tokens_cache_write INTEGER, tokens_cache_read INTEGER,
  cost_usd REAL,
  recorded_at INTEGER NOT NULL,
  PRIMARY KEY(job_id, step));
