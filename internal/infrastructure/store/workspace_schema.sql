-- PostgreSQL. Apply after 001_initial.sql and 002_local_auth.sql.
-- Logical namespaces are platform resources; this migration creates no Kubernetes namespaces.
CREATE TABLE IF NOT EXISTS user_namespaces (
 id text PRIMARY KEY, owner_id text NOT NULL UNIQUE, name text NOT NULL,
 create_at timestamptz NOT NULL DEFAULT now(), update_at timestamptz NOT NULL DEFAULT now(), create_by text NOT NULL, update_by text NOT NULL,
 UNIQUE(id,owner_id)
);
CREATE TABLE IF NOT EXISTS workspaces (
 id text PRIMARY KEY, namespace_id text NOT NULL, owner_id text NOT NULL, project_id text NOT NULL REFERENCES projects(id),
 name text NOT NULL CHECK(length(name) BETWEEN 1 AND 80), request_key text NOT NULL,
 state text NOT NULL DEFAULT 'Creating' CHECK(state IN ('Creating','Available','Error')),
 cluster_id text NOT NULL, kube_namespace text NOT NULL, pvc_name text NOT NULL, pvc_uid text NOT NULL DEFAULT '', pv_name text NOT NULL DEFAULT '',
 storage_class text NOT NULL, requested_bytes bigint NOT NULL CHECK(requested_bytes > 0), storage_phase text NOT NULL DEFAULT 'Unknown',
 message text NOT NULL DEFAULT '', active_sandbox_id text UNIQUE REFERENCES sandboxes(id), archived_at timestamptz, last_observed_at timestamptz,
 create_at timestamptz NOT NULL DEFAULT now(), update_at timestamptz NOT NULL DEFAULT now(), create_by text NOT NULL, update_by text NOT NULL,
 FOREIGN KEY(namespace_id,owner_id) REFERENCES user_namespaces(id,owner_id),
 UNIQUE(project_id,owner_id,request_key), UNIQUE(cluster_id,kube_namespace,pvc_name), UNIQUE(id,project_id,owner_id)
);
CREATE INDEX IF NOT EXISTS workspaces_project_owner ON workspaces(project_id,owner_id,create_at DESC);
CREATE INDEX IF NOT EXISTS workspaces_namespace ON workspaces(namespace_id,create_at DESC);
ALTER TABLE sandboxes ADD COLUMN IF NOT EXISTS workspace_id text REFERENCES workspaces(id);
CREATE TABLE IF NOT EXISTS conversation_refs (
 id text PRIMARY KEY, namespace_id text NOT NULL, project_id text NOT NULL REFERENCES projects(id), owner_id text NOT NULL,
 integration_id text NOT NULL CHECK(length(integration_id) BETWEEN 1 AND 80), external_id text NOT NULL CHECK(length(external_id) BETWEEN 1 AND 255), title text NOT NULL CHECK(length(title) BETWEEN 1 AND 160),
 workspace_id text, version bigint NOT NULL DEFAULT 0 CHECK(version >= 0),
 create_at timestamptz NOT NULL DEFAULT now(), update_at timestamptz NOT NULL DEFAULT now(), create_by text NOT NULL, update_by text NOT NULL,
 FOREIGN KEY(namespace_id,owner_id) REFERENCES user_namespaces(id,owner_id),
 FOREIGN KEY(workspace_id,project_id,owner_id) REFERENCES workspaces(id,project_id,owner_id),
 UNIQUE(integration_id,project_id,owner_id,external_id)
);
CREATE INDEX IF NOT EXISTS conversations_owner ON conversation_refs(project_id,owner_id,create_at DESC);
CREATE TABLE IF NOT EXISTS conversation_binding_history (
 id bigserial PRIMARY KEY, conversation_id text NOT NULL REFERENCES conversation_refs(id), workspace_id text REFERENCES workspaces(id), version bigint NOT NULL,
 create_at timestamptz NOT NULL DEFAULT now(), update_at timestamptz NOT NULL DEFAULT now(), create_by text NOT NULL, update_by text NOT NULL,
 UNIQUE(conversation_id,version)
);
ALTER TABLE audit_events ADD COLUMN IF NOT EXISTS workspace_id text NOT NULL DEFAULT '';
ALTER TABLE audit_events ADD COLUMN IF NOT EXISTS conversation_id text NOT NULL DEFAULT '';
-- Preserve project/user ownership even if a future caller bypasses the application helpers.
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='sandboxes'::regclass AND conname='sandbox_workspace_owner_fk') THEN
  ALTER TABLE sandboxes ADD CONSTRAINT sandbox_workspace_owner_fk FOREIGN KEY(workspace_id,project_id,owner_id) REFERENCES workspaces(id,project_id,owner_id);
 END IF;
END $$;
