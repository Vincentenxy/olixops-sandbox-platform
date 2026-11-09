-- PostgreSQL; apply after 003_workspaces.sql. No PVC/PV deletion or data movement.
ALTER TABLE workspaces ADD COLUMN IF NOT EXISTS attachment_generation bigint NOT NULL DEFAULT 0;
CREATE TABLE IF NOT EXISTS run_contexts (
 id text PRIMARY KEY, project_id text NOT NULL REFERENCES projects(id), owner_id text NOT NULL,
 actor text NOT NULL, conversation_id text NOT NULL REFERENCES conversation_refs(id), workspace_id text NOT NULL,
 binding_version bigint NOT NULL, sandbox_id text REFERENCES sandboxes(id), attachment_generation bigint NOT NULL DEFAULT 0,
 image text NOT NULL, ttl integer NOT NULL CHECK(ttl >= 60),
 state text NOT NULL DEFAULT 'Preparing' CHECK(state IN ('Preparing','Waiting','Ready','Failed','Released','Invalidated')),
 reason text NOT NULL DEFAULT '', message text NOT NULL DEFAULT '', released_at timestamptz,
 create_at timestamptz NOT NULL DEFAULT now(), update_at timestamptz NOT NULL DEFAULT now(), create_by text NOT NULL, update_by text NOT NULL,
 FOREIGN KEY(workspace_id,project_id,owner_id) REFERENCES workspaces(id,project_id,owner_id)
);
CREATE UNIQUE INDEX IF NOT EXISTS run_workspace_active ON run_contexts(workspace_id) WHERE released_at IS NULL;
CREATE UNIQUE INDEX IF NOT EXISTS run_conversation_active ON run_contexts(conversation_id) WHERE released_at IS NULL;
CREATE INDEX IF NOT EXISTS run_project_actor ON run_contexts(project_id,actor,create_at DESC);
CREATE TABLE IF NOT EXISTS run_requests (
 project_id text NOT NULL REFERENCES projects(id), actor text NOT NULL, request_key text NOT NULL,
 run_id text NOT NULL REFERENCES run_contexts(id),
 create_at timestamptz NOT NULL DEFAULT now(), update_at timestamptz NOT NULL DEFAULT now(), create_by text NOT NULL, update_by text NOT NULL,
 PRIMARY KEY(project_id,actor,request_key)
);
CREATE TABLE IF NOT EXISTS runtime_operations (
 id text PRIMARY KEY REFERENCES run_contexts(id),
 state text NOT NULL DEFAULT 'Pending' CHECK(state IN ('Pending','Preparing','Waiting','Ready','Failed','Cancelled')),
 reason text NOT NULL DEFAULT '', message text NOT NULL DEFAULT '', attempt integer NOT NULL DEFAULT 0,
 generation bigint NOT NULL DEFAULT 0, lease_owner text NOT NULL DEFAULT '', lease_until timestamptz,
 next_attempt_at timestamptz NOT NULL DEFAULT now(), create_sent_at timestamptz, owns_creation boolean NOT NULL DEFAULT false,
 create_at timestamptz NOT NULL DEFAULT now(), update_at timestamptz NOT NULL DEFAULT now(), create_by text NOT NULL, update_by text NOT NULL
);
CREATE INDEX IF NOT EXISTS runtime_operations_due ON runtime_operations(next_attempt_at) WHERE state IN ('Pending','Preparing','Waiting','Ready');
ALTER TABLE audit_events ADD COLUMN IF NOT EXISTS run_context_id text NOT NULL DEFAULT '';
