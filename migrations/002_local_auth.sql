CREATE TABLE IF NOT EXISTS local_users (
 user_id text PRIMARY KEY, username text NOT NULL UNIQUE, name text NOT NULL, password_hash text NOT NULL,
 enabled boolean NOT NULL DEFAULT true,
 create_at timestamptz NOT NULL DEFAULT now(), update_at timestamptz NOT NULL DEFAULT now(), create_by text NOT NULL, update_by text NOT NULL
);
CREATE TABLE IF NOT EXISTS local_sessions (
 id text PRIMARY KEY, user_id text NOT NULL REFERENCES local_users(user_id), expires_at timestamptz NOT NULL,
 create_at timestamptz NOT NULL DEFAULT now(), update_at timestamptz NOT NULL DEFAULT now(), create_by text NOT NULL, update_by text NOT NULL
);
CREATE INDEX IF NOT EXISTS local_sessions_expiry ON local_sessions(expires_at);
