CREATE TABLE IF NOT EXISTS llm_backends (
    id VARCHAR(36) PRIMARY KEY,
    name VARCHAR(255) NOT NULL UNIQUE,
    base_url TEXT NOT NULL,
    type VARCHAR(255) NOT NULL,

    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL
);

CREATE TABLE IF NOT EXISTS ollama_models (
    id VARCHAR(36),
    model VARCHAR(255) NOT NULL UNIQUE,

    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL
);

CREATE TABLE IF NOT EXISTS users (
    id VARCHAR(255) PRIMARY KEY,
    friendly_name TEXT,
    email TEXT NOT NULL UNIQUE,
    subject TEXT NOT NULL UNIQUE,
    hashed_password TEXT,
    recovery_code_hash TEXT,
    salt TEXT,

    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL
);

CREATE TABLE IF NOT EXISTS accesslists (
    id VARCHAR(36) PRIMARY KEY,

    identity VARCHAR(255) NOT NULL,
    resource VARCHAR(255) NOT NULL,
    permission INT NOT NULL,

    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL
);

CREATE TABLE IF NOT EXISTS job_queue_v2 (
    id VARCHAR(255) PRIMARY KEY,
    task_type VARCHAR(255) NOT NULL,
    payload JSONB NOT NULL,

    scheduled_for INT,
    valid_until INT,
    created_at TIMESTAMP NOT NULL
);

CREATE TABLE IF NOT EXISTS files (
    id VARCHAR(36) PRIMARY KEY,
    path TEXT NOT NULL,
    type VARCHAR(255) NOT NULL,

    meta JSONB NOT NULL,
    blobs_id TEXT,

    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL

);

CREATE TABLE IF NOT EXISTS blobs (
    id VARCHAR(36) PRIMARY KEY,
    meta JSONB NOT NULL,

    data bytea NOT NULL,

    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_job_queue_v2_task_type ON job_queue_v2 USING hash(task_type);
CREATE INDEX IF NOT EXISTS idx_accesslists_identity ON accesslists USING hash(identity);
CREATE INDEX IF NOT EXISTS idx_users_email ON users USING hash(email);
CREATE INDEX IF NOT EXISTS idx_users_subject ON users USING hash(subject);
-- ALTER TABLE users ADD COLUMN IF NOT EXISTS salt TEXT;

-- For pagination --
CREATE INDEX IF NOT EXISTS idx_users_created_at ON users (created_at);
CREATE INDEX IF NOT EXISTS idx_accesslists_created_at ON accesslists (created_at);

-- For filesystem --
CREATE INDEX IF NOT EXISTS idx_files_path ON files (path);

-- CREATE INDEX IF NOT EXISTS idx_files_created_at ON files (created_at);
-- CREATE INDEX IF NOT EXISTS idx_blobs_created_at ON blobs (created_at);
