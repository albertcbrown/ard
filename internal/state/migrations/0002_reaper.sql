ALTER TABLE jobs ADD COLUMN branch TEXT;
ALTER TABLE jobs ADD COLUMN retry_of TEXT;
ALTER TABLE jobs ADD COLUMN auto_retry INTEGER NOT NULL DEFAULT 0;
ALTER TABLE jobs ADD COLUMN kill_reason TEXT;
CREATE INDEX jobs_cleanup ON jobs(status, cleaned_at);
