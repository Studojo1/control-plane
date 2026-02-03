-- Add progress tracking fields to jobs table
ALTER TABLE cp.jobs
    ADD COLUMN IF NOT EXISTS progress INTEGER,
    ADD COLUMN IF NOT EXISTS current_section TEXT;

-- Add index for progress queries
CREATE INDEX IF NOT EXISTS idx_jobs_progress ON cp.jobs (progress) WHERE progress IS NOT NULL;

