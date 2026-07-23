CREATE TABLE IF NOT EXISTS jobs (
    id INTEGER PRIMARY KEY,
    source TEXT NOT NULL,
    external_id TEXT NOT NULL,
    url TEXT NOT NULL,
    title TEXT NOT NULL,
    company_name TEXT NOT NULL,
    company_info TEXT NOT NULL,
    description TEXT,
    salary_min INTEGER,
    salary_max INTEGER,
    location TEXT NOT NULL,
    remote_type TEXT NOT NULL,
    content_hash TEXT,
    process_state TEXT NOT NULL,
    filter_hits TEXT,
    profile_revision TEXT,
    apply_state TEXT,
    discovered_by_run_id INTEGER REFERENCES runs(id),
    first_seen_at TEXT NOT NULL,
    last_seen_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    UNIQUE(source, external_id)
);

CREATE INDEX IF NOT EXISTS jobs_process_state_idx ON jobs(process_state);
CREATE INDEX IF NOT EXISTS jobs_apply_state_idx ON jobs(apply_state);
CREATE INDEX IF NOT EXISTS jobs_discovered_by_run_idx ON jobs(discovered_by_run_id);

CREATE TABLE IF NOT EXISTS scores (
    id INTEGER PRIMARY KEY,
    job_id INTEGER NOT NULL REFERENCES jobs(id),
    dim_hard_skill INTEGER NOT NULL,
    dim_domain INTEGER NOT NULL,
    dim_seniority INTEGER NOT NULL,
    dim_condition INTEGER NOT NULL,
    dim_direction INTEGER NOT NULL,
    total REAL NOT NULL,
    reason TEXT NOT NULL,
    runner TEXT NOT NULL,
    profile_revision TEXT,
    created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS letters (
    id INTEGER PRIMARY KEY,
    job_id INTEGER NOT NULL REFERENCES jobs(id),
    content TEXT NOT NULL,
    status TEXT NOT NULL,
    rounds INTEGER NOT NULL,
    review_log TEXT NOT NULL,
    runner_draft TEXT NOT NULL,
    runner_review TEXT NOT NULL,
    profile_revision TEXT,
    created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS status_events (
    id INTEGER PRIMARY KEY,
    job_id INTEGER NOT NULL REFERENCES jobs(id),
    axis TEXT NOT NULL,
    from_state TEXT NOT NULL,
    to_state TEXT NOT NULL,
    note TEXT,
    created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS runs (
    id INTEGER PRIMARY KEY,
    started_at TEXT NOT NULL,
    finished_at TEXT,
    trigger TEXT NOT NULL,
    stats TEXT NOT NULL,
    error TEXT
);

CREATE TABLE IF NOT EXISTS agent_calls (
    id INTEGER PRIMARY KEY,
    job_id INTEGER REFERENCES jobs(id),
    role TEXT NOT NULL,
    runner TEXT NOT NULL,
    input TEXT NOT NULL,
    output TEXT NOT NULL,
    ok INTEGER NOT NULL,
    duration_ms INTEGER NOT NULL,
    profile_revision TEXT,
    created_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS agent_calls_role_created_idx ON agent_calls(role, created_at);
