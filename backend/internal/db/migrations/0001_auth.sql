-- goose down: drop sessions first (FK dependency)
-- +goose Up
CREATE TABLE users (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    username      TEXT    NOT NULL UNIQUE,
    password_hash TEXT    NOT NULL,
    auth_generation INTEGER NOT NULL DEFAULT 0,
    created_at    INTEGER NOT NULL
);

CREATE TABLE bootstrap_tokens (
    token_hash TEXT    PRIMARY KEY,
    expires_at INTEGER NOT NULL,
    used_at    INTEGER,
    created_at INTEGER NOT NULL
);

CREATE TABLE sessions (
    id_hash    TEXT    PRIMARY KEY,
    user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    expires_at INTEGER NOT NULL,
    created_at INTEGER NOT NULL
);

CREATE INDEX idx_sessions_expires ON sessions(expires_at);
CREATE INDEX idx_bootstrap_tokens_expires ON bootstrap_tokens(expires_at);

-- +goose Down
DROP TABLE sessions;
DROP TABLE bootstrap_tokens;
DROP TABLE users;
