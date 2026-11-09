# PostgreSQL 数据库设计

SQL 与 migrations/001_initial.sql、内嵌 schema.sql 保持一致。所有表包含 create_at/update_at/create_by/update_by。

```sql
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
```

projects：项目及活跃实例配额；行锁串行化配额预留。

project_members：项目/员工复合主键，owner/maintainer/member。

sandboxes：平台 ID 与远端 backend_id 分离；project_id/owner_id/request_key 唯一索引保障幂等。project_id/state 索引用于配额统计。

audit_events：记录 requested 与最终结果，project_id/create_at 倒序索引用于查询。

本版使用初始化幂等 DDL；后续结构变更需新建编号迁移，不依赖 CREATE TABLE IF NOT EXISTS 更新已有字段。

## 本地认证迁移（002_local_auth.sql）

```sql
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
```

local_users 的 username 唯一，user_id 对应 JWT staffuserid 和项目成员身份；password_hash 仅存 bcrypt。local_sessions 的 id 对应 JWT jti，过期索引用于清理，退出删除当前会话行。

## 持久工作区迁移（003_workspaces.sql）

用户逻辑 Namespace 不创建 Kubernetes Namespace。每用户一个 Namespace，项目内的多个 Workspace 归属它；对话通过 Workspace 间接定位 PVC。

```sql
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
```

user_namespaces.owner_id 唯一，身份不依赖可修改的用户名。Namespace/owner 复合外键阻止工作区和对话归属不一致。workspaces 的项目/owner/request_key 保障幂等；目标集群/Namespace/PVC 名唯一。active_sandbox_id 是持久独占预留，不按超时自动清除。

conversation_refs 外部键按集成/项目/用户隔离；workspace/project/owner 复合外键阻止跨用户绑定。绑定更新检查 version，历史表按 conversation/version 唯一。sandbox_workspace_owner_fk 使沙箱挂载关系在数据库层也保持同项目/同 owner。

首次访问或创建工作区时自动登记个人逻辑空间；外部 JWT 用户无需在 local_users 存在，因此 Namespace 不外键到本地认证账号。已有临时沙箱的 workspace_id 为 NULL，保持兼容。

## 对话运行迁移（004_conversation_runtime.sql）

```sql
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
```

run_contexts 固定操作者、对话、绑定版本、工作区、实例及挂载代次。两个部分唯一索引保证同一工作区和同一对话最多一个未释放上下文；释放后历史继续保留。run_requests 按项目/操作者/请求键返回原结果。

runtime_operations 与 RunContext 一对一。next_attempt_at 支撑可恢复的后台领取，generation/lease_owner/lease_until 拒绝迟到 worker 写入；create_sent_at 在远端调用前提交，结果未知时只核对 metadata，不重复创建。owns_creation 区分本任务创建与复用已有实例。Ready 任务也定期核对实例是否终止。

workspaces.attachment_generation 仅在预留新的计算实例时递增。工具请求必须匹配绑定版本和挂载代次；工作区 advisory transaction lock 使前台工具与绑定/预留/释放互斥。后台进程不属于前台请求互斥范围。audit_events.run_context_id 串联执行和工具操作，记录真实 actor。
