CREATE TABLE IF NOT EXISTS github_repos (
    id VARCHAR(255) PRIMARY KEY,
    user_id VARCHAR(512) NOT NULL,
    bot_user_name VARCHAR(512) NOT NULL,
    owner VARCHAR(512) NOT NULL,
    repo_name VARCHAR(512) NOT NULL,
    access_token VARCHAR(512) NOT NULL,
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL,
    UNIQUE (user_id, owner, repo_name)
);

CREATE TABLE bots (
    id VARCHAR(255) PRIMARY KEY,
    name VARCHAR(512) NOT NULL UNIQUE,
    user_id VARCHAR(512) NOT NULL,
    bot_type VARCHAR(512) NOT NULL,
    state bytea NOT NULL,
    job_type VARCHAR(512) NOT NULL,
    task_chain_id VARCHAR(512) NOT NULL,

    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL
);

CREATE TABLE IF NOT EXISTS ollama_models (
    id VARCHAR(255) PRIMARY KEY,
    model VARCHAR(512) NOT NULL UNIQUE,

    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL
);

CREATE TABLE IF NOT EXISTS llm_pool (
    id VARCHAR(255) PRIMARY KEY,
    name VARCHAR(512) NOT NULL UNIQUE,
    purpose_type VARCHAR(512) NOT NULL,

    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL
);

CREATE TABLE IF NOT EXISTS llm_backends (
    id VARCHAR(255) PRIMARY KEY,
    name VARCHAR(512) NOT NULL UNIQUE,
    base_url VARCHAR(512) NOT NULL UNIQUE,
    type VARCHAR(512) NOT NULL,

    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL
);

CREATE TABLE IF NOT EXISTS llm_pool_backend_assignments (
    pool_id VARCHAR(255) NOT NULL REFERENCES llm_pool(id) ON DELETE CASCADE,
    backend_id VARCHAR(255) NOT NULL REFERENCES llm_backends(id) ON DELETE CASCADE,
    PRIMARY KEY (pool_id, backend_id),
    assigned_at TIMESTAMP NOT NULL
);

CREATE TABLE IF NOT EXISTS ollama_model_assignments (
    model_id VARCHAR(255) NOT NULL REFERENCES ollama_models(id) ON DELETE CASCADE,
    llm_pool_id VARCHAR(255) NOT NULL REFERENCES llm_pool(id) ON DELETE CASCADE,
    PRIMARY KEY (model_id, llm_pool_id),

    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL
);

CREATE TABLE IF NOT EXISTS users (
    id VARCHAR(512) PRIMARY KEY,
    friendly_name VARCHAR(512),
    email VARCHAR(512) NOT NULL UNIQUE,
    subject VARCHAR(512) NOT NULL UNIQUE,
    hashed_password VARCHAR(2048),
    recovery_code_hash VARCHAR(2048),
    salt VARCHAR(2048),

    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL
);

CREATE TABLE IF NOT EXISTS accesslists (
    id VARCHAR(255) PRIMARY KEY,

    identity VARCHAR(512) NOT NULL REFERENCES users(subject) ON DELETE CASCADE,
    resource VARCHAR(512) NOT NULL,
    resource_type VARCHAR(512) NOT NULL,
    permission INT NOT NULL,

    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL
);

CREATE TABLE IF NOT EXISTS leased_jobs (
    id VARCHAR(255) PRIMARY KEY,
    task_type VARCHAR(512) NOT NULL,
    operation VARCHAR(512),
    subject VARCHAR(512),
    entity_id VARCHAR(512),
    entity_type VARCHAR(512),

    payload JSONB NOT NULL,

    scheduled_for INT,
    valid_until INT,
    retry_count INT NOT NULL DEFAULT 0,
    created_at TIMESTAMP NOT NULL,

    lease_duration INT NOT NULL,
    leaser VARCHAR(512) NOT NULL,
    lease_expiration TIMESTAMP NOT NULL
);

CREATE TABLE IF NOT EXISTS job_queue_v2 (
    id VARCHAR(255) PRIMARY KEY,
    task_type VARCHAR(512) NOT NULL,
    operation VARCHAR(512),
    subject VARCHAR(512),
    entity_id VARCHAR(512),
    entity_type VARCHAR(512),
    payload JSONB NOT NULL,

    scheduled_for INT,
    valid_until INT,
    retry_count INT NOT NULL DEFAULT 0,
    created_at TIMESTAMP NOT NULL
);

CREATE TABLE IF NOT EXISTS entity_events (
    id VARCHAR(255) PRIMARY KEY,
    entity_id VARCHAR(255) NOT NULL,
    entity_type VARCHAR(255) NOT NULL,
    created_at TIMESTAMP NOT NULL,
    processed_at TIMESTAMP,
    error TEXT
);

CREATE TABLE IF NOT EXISTS chunks_idx (
    id VARCHAR(255) PRIMARY KEY,
    vector_id VARCHAR(255) NOT NULL,
    vector_store VARCHAR(255) NOT NULL,
    embedding_model VARCHAR(255) NOT NULL,

    resource_id VARCHAR(255) NOT NULL,
    resource_type VARCHAR(255) NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_chunks_idx_vector_id ON chunks_idx USING hash(vector_id);


CREATE TABLE IF NOT EXISTS files (
    id VARCHAR(255) PRIMARY KEY,
    type VARCHAR(512) NOT NULL,

    meta JSONB NOT NULL,
    blobs_id VARCHAR(255),
    is_folder BOOLEAN DEFAULT FALSE,

    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL
);

CREATE TABLE IF NOT EXISTS filestree (
    id VARCHAR(255) PRIMARY KEY,
    parent_id VARCHAR(255),
    name VARCHAR(1024) NOT NULL,

    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL,
    UNIQUE (parent_id, name)
);

CREATE INDEX IF NOT EXISTS idx_filetree_list ON filestree (name, parent_id);
CREATE INDEX IF NOT EXISTS idx_filetree_parent_id ON filestree USING hash(parent_id);

CREATE TABLE IF NOT EXISTS blobs (
    id VARCHAR(255) PRIMARY KEY,
    meta JSONB NOT NULL,

    data bytea NOT NULL,

    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL
);

CREATE TABLE IF NOT EXISTS message_indices (
    id VARCHAR(255) PRIMARY KEY,
    identity VARCHAR(512) NOT NULL REFERENCES users(subject) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS messages (
    id VARCHAR(255),
    idx_id VARCHAR(255) NOT NULL REFERENCES message_indices(id) ON DELETE CASCADE,

    payload JSONB NOT NULL,
    added_at TIMESTAMP NOT NULL,

    PRIMARY KEY (id, idx_id)
);

CREATE TABLE IF NOT EXISTS kv (
    key VARCHAR(255) PRIMARY KEY,
    value JSONB NOT NULL,

    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL
);


CREATE TABLE IF NOT EXISTS telegram_frontends (
    id VARCHAR(255) PRIMARY KEY,
    user_id VARCHAR(512) REFERENCES users(id),
    chat_chain VARCHAR(512) NOT NULL,
    description VARCHAR(512),
    bot_token VARCHAR(512) NOT NULL UNIQUE,
    sync_interval INT NOT NULL,
    status VARCHAR(50) NOT NULL DEFAULT 'active',
    last_offset INT NOT NULL DEFAULT 0,
    last_heartbeat TIMESTAMP,
    last_error TEXT,

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
-- CREATE INDEX IF NOT EXISTS idx_files_path ON files (path);
ALTER TABLE accesslists ADD CONSTRAINT fk_accesslists_identity FOREIGN KEY (identity) REFERENCES users(subject) ON DELETE CASCADE;

-- CREATE INDEX IF NOT EXISTS idx_files_created_at ON files (created_at);
-- CREATE INDEX IF NOT EXISTS idx_blobs_created_at ON blobs (created_at);

CREATE OR REPLACE FUNCTION estimate_row_count(table_name TEXT)
RETURNS BIGINT AS $$
DECLARE
    result BIGINT;
BEGIN
    SELECT reltuples::BIGINT
    INTO result
    FROM pg_class
    WHERE relname = table_name;

    RETURN COALESCE(result, 0);
END;
$$ LANGUAGE plpgsql STABLE;
