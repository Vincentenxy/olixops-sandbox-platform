CREATE TABLE IF NOT EXISTS projects (
 id text PRIMARY KEY, name text NOT NULL CHECK(length(name) BETWEEN 1 AND 80), quota integer NOT NULL CHECK(quota > 0),
 create_at timestamptz NOT NULL DEFAULT now(), update_at timestamptz NOT NULL DEFAULT now(), create_by text NOT NULL, update_by text NOT NULL
);
CREATE TABLE IF NOT EXISTS project_members (
 project_id text NOT NULL REFERENCES projects(id), user_id text NOT NULL, role text NOT NULL CHECK(role IN ('owner','maintainer','member')),
 create_at timestamptz NOT NULL DEFAULT now(), update_at timestamptz NOT NULL DEFAULT now(), create_by text NOT NULL, update_by text NOT NULL,
 PRIMARY KEY(project_id,user_id)
);
CREATE TABLE IF NOT EXISTS sandboxes (
 id text PRIMARY KEY, project_id text NOT NULL REFERENCES projects(id), owner_id text NOT NULL, name text NOT NULL, image text NOT NULL,
 backend_id text NOT NULL DEFAULT '', state text NOT NULL, request_key text NOT NULL, ttl integer NOT NULL, expires_at timestamptz,
 create_at timestamptz NOT NULL DEFAULT now(), update_at timestamptz NOT NULL DEFAULT now(), create_by text NOT NULL, update_by text NOT NULL,
 UNIQUE(project_id,owner_id,request_key)
);
CREATE INDEX IF NOT EXISTS sandboxes_project_state ON sandboxes(project_id,state);
CREATE TABLE IF NOT EXISTS audit_events (
 id bigserial PRIMARY KEY, project_id text NOT NULL REFERENCES projects(id), sandbox_id text NOT NULL DEFAULT '', action text NOT NULL,
 actor text NOT NULL, outcome text NOT NULL,
 create_at timestamptz NOT NULL DEFAULT now(), update_at timestamptz NOT NULL DEFAULT now(), create_by text NOT NULL, update_by text NOT NULL
);
CREATE INDEX IF NOT EXISTS audit_project_time ON audit_events(project_id,create_at DESC);
