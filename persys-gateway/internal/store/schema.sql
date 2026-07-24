-- Replaces MongoDB's users/sessions/webhooks collections. Only three
-- tables: this is everything the gateway's code actually reads or
-- writes today.
--
-- Deliberately absent:
--   - "repos": the old Mongo "repos" collection was created at startup
--     but never read from or written to anywhere in the codebase —
--     persys-forgery owns repository data via its own gRPC API
--     (ListUserRepositories, RegisterWebhook). Carrying an unused table
--     forward would just be new cruft in a new database.
--   - "cluster_state": was persisted to Mongo every 15s, but
--     ClusterMetaController already serves cluster state from the
--     in-memory scheduler pool directly (SnapshotClusters) — nothing
--     ever read the persisted copy back. Dropped rather than migrated.
--   - "cluster_owners" (multi-tenancy): designed in an earlier pass but
--     not wired into any code path yet — add it in its own migration
--     once managed-mode ownership checks are actually implemented,
--     rather than shipping an unused table now.

CREATE TABLE IF NOT EXISTS users (
    user_id       BIGINT PRIMARY KEY,   -- GitHub's numeric user ID
    login         TEXT NOT NULL,
    name          TEXT NOT NULL DEFAULT '',
    email         TEXT NOT NULL DEFAULT '',
    company       TEXT NOT NULL DEFAULT '',
    url           TEXT NOT NULL DEFAULT '',
    github_token  TEXT NOT NULL DEFAULT '',
    persys_token  TEXT NOT NULL DEFAULT '',
    state         TEXT NOT NULL DEFAULT '',
    status        TEXT NOT NULL DEFAULT '',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_users_state ON users(state) WHERE state <> '';

-- OAuth CSRF state. Was a package-level Go variable in the original
-- code, overwritten on every login attempt — a real race under
-- concurrent logins. A row per in-flight attempt, keyed by the state
-- token itself, closes that outright.
CREATE TABLE IF NOT EXISTS oauth_sessions (
    state       TEXT PRIMARY KEY,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at  TIMESTAMPTZ NOT NULL,
    consumed    BOOLEAN NOT NULL DEFAULT false
);

CREATE TABLE IF NOT EXISTS webhook_events (
    delivery_id      TEXT PRIMARY KEY,
    event_name       TEXT NOT NULL,
    repository       TEXT NOT NULL,
    cluster_id       TEXT NOT NULL DEFAULT '',
    verified         BOOLEAN NOT NULL DEFAULT false,
    status           TEXT NOT NULL,
    attempts         INT NOT NULL DEFAULT 0,
    last_error       TEXT NOT NULL DEFAULT '',
    received_at      TIMESTAMPTZ,
    next_retry_at    TIMESTAMPTZ,
    last_updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
