CREATE TABLE secret (
    id VARCHAR PRIMARY KEY NOT NULL,
    secret_text VARCHAR NOT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    expires_at TIMESTAMP NULL,
    remaining_views INTEGER NOT NULL
);