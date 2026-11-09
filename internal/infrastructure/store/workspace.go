package store

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"olixops-sandbox-platform/internal/domain"
)

func (s *Store) Namespace(ctx context.Context, owner string) (domain.Namespace, error) {
	var n domain.Namespace
	id := ID()
	e := s.DB.QueryRow(ctx, `INSERT INTO user_namespaces(id,owner_id,name,create_by,update_by) VALUES($1,$2,$3,$2,$2) ON CONFLICT(owner_id) DO UPDATE SET owner_id=excluded.owner_id RETURNING id,owner_id,name`, id, owner, "personal-"+id[:12]).Scan(&n.ID, &n.OwnerID, &n.Name)
	return n, e
}

const workspaceCols = `id,namespace_id,owner_id,project_id,name,request_key,state,cluster_id,kube_namespace,pvc_name,pvc_uid,pv_name,storage_class,requested_bytes,storage_phase,message,COALESCE(active_sandbox_id,''),archived_at,last_observed_at,create_at,attachment_generation`

func scanWorkspace(row pgx.Row) (domain.Workspace, error) {
	var w domain.Workspace
	e := row.Scan(&w.ID, &w.NamespaceID, &w.OwnerID, &w.ProjectID, &w.Name, &w.RequestKey, &w.State, &w.ClusterID, &w.KubeNamespace, &w.PVCName, &w.PVCUID, &w.PVName, &w.StorageClass, &w.RequestedBytes, &w.StoragePhase, &w.Message, &w.ActiveSandboxID, &w.ArchivedAt, &w.LastObservedAt, &w.CreateAt, &w.AttachmentGeneration)
	return w, e
}
func (s *Store) Workspace(ctx context.Context, id string) (domain.Workspace, error) {
	return scanWorkspace(s.DB.QueryRow(ctx, "SELECT "+workspaceCols+" FROM workspaces WHERE id=$1", id))
}
func (s *Store) Workspaces(ctx context.Context, p, u string, all bool) ([]domain.Workspace, error) {
	rows, e := s.DB.Query(ctx, "SELECT "+workspaceCols+" FROM workspaces WHERE project_id=$1 AND (owner_id=$2 OR $3) ORDER BY create_at DESC LIMIT 200", p, u, all)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []domain.Workspace{}
	for rows.Next() {
		w, e := scanWorkspace(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, w)
	}
	return out, rows.Err()
}
func (s *Store) ReserveWorkspace(ctx context.Context, w domain.Workspace, quota int, maxBytes int64) (domain.Workspace, error) {
	n, e := s.Namespace(ctx, w.OwnerID)
	if e != nil {
		return w, e
	}
	w.NamespaceID = n.ID
	tx, e := s.DB.Begin(ctx)
	if e != nil {
		return w, e
	}
	defer tx.Rollback(ctx)
	var p string
	if e = tx.QueryRow(ctx, "SELECT id FROM projects WHERE id=$1 FOR UPDATE", w.ProjectID).Scan(&p); e != nil {
		return w, e
	}
	old, e := scanWorkspace(tx.QueryRow(ctx, "SELECT "+workspaceCols+" FROM workspaces WHERE project_id=$1 AND owner_id=$2 AND request_key=$3", w.ProjectID, w.OwnerID, w.RequestKey))
	if e == nil {
		if old.Name != w.Name || old.StorageClass != w.StorageClass || old.RequestedBytes != w.RequestedBytes {
			return old, ErrConflict
		}
		return old, tx.Commit(ctx)
	}
	if !errors.Is(e, pgx.ErrNoRows) {
		return w, e
	}
	var count int
	var bytes int64
	if e = tx.QueryRow(ctx, "SELECT count(*),COALESCE(sum(requested_bytes),0) FROM workspaces WHERE project_id=$1", w.ProjectID).Scan(&count, &bytes); e != nil {
		return w, e
	}
	if count >= quota || bytes+w.RequestedBytes > maxBytes {
		return w, ErrQuota
	}
	w.ID = ID()
	w.PVCName = "olx-ws-" + w.ID
	_, e = tx.Exec(ctx, `INSERT INTO workspaces(id,namespace_id,owner_id,project_id,name,request_key,cluster_id,kube_namespace,pvc_name,storage_class,requested_bytes,create_by,update_by) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$3,$3)`, w.ID, w.NamespaceID, w.OwnerID, w.ProjectID, w.Name, w.RequestKey, w.ClusterID, w.KubeNamespace, w.PVCName, w.StorageClass, w.RequestedBytes)
	if e != nil {
		return w, e
	}
	if e = resourceAudit(ctx, tx, w.ProjectID, w.ID, "", "workspace.create", w.OwnerID); e != nil {
		return w, e
	}
	if e = tx.Commit(ctx); e != nil {
		return w, e
	}
	return s.Workspace(ctx, w.ID)
}
func resourceAudit(ctx context.Context, tx pgx.Tx, p, w, c, action, actor string) error {
	_, e := tx.Exec(ctx, `INSERT INTO audit_events(project_id,workspace_id,conversation_id,action,actor,outcome,create_by,update_by) VALUES($1,$2,$3,$4,$5,'success',$5,$5)`, p, w, c, action, actor)
	return e
}

// SyncWorkspaceStorage serializes Kubernetes observation and first provisioning across replicas.
// The callback receives a fresh row while holding its lock and only changes storage fields.
// Storage errors are persisted before returning, so admission fails closed.
func (s *Store) SyncWorkspaceStorage(ctx context.Context, id string, observe func(*domain.Workspace) error) (domain.Workspace, error) {
	tx, e := s.DB.Begin(ctx)
	if e != nil {
		return domain.Workspace{}, e
	}
	defer tx.Rollback(ctx)
	w, e := scanWorkspace(tx.QueryRow(ctx, "SELECT "+workspaceCols+" FROM workspaces WHERE id=$1 FOR UPDATE", id))
	if e != nil {
		return w, e
	}
	observationErr := observe(&w)
	tag, e := tx.Exec(ctx, observeWorkspaceSQL, w.ID, w.PVCUID, w.PVName, w.StoragePhase, w.State, w.Message)
	if e != nil {
		return w, e
	}
	if tag.RowsAffected() != 1 {
		return w, ErrConflict
	}
	if e = tx.Commit(ctx); e != nil {
		return w, e
	}
	return w, observationErr
}

const observeWorkspaceSQL = `UPDATE workspaces SET pvc_uid=CASE WHEN pvc_uid='' THEN $2 ELSE pvc_uid END,pv_name=$3,storage_phase=$4,state=$5,message=$6,last_observed_at=now(),update_at=now() WHERE id=$1 AND (pvc_uid='' OR pvc_uid=$2 OR $2='')`

// Original PVC UID is immutable once observed. A same-name replacement requires operator review.
func (s *Store) ObserveWorkspace(ctx context.Context, id, uid, pv, phase, state, message string) error {
	tag, e := s.DB.Exec(ctx, observeWorkspaceSQL, id, uid, pv, phase, state, message)
	if e == nil && tag.RowsAffected() == 0 {
		return ErrConflict
	}
	return e
}
func (s *Store) WorkspaceMessage(ctx context.Context, id, message string) error {
	_, e := s.DB.Exec(ctx, "UPDATE workspaces SET message=$2,update_at=now() WHERE id=$1", id, message)
	return e
}
func (s *Store) ArchiveWorkspace(ctx context.Context, id, actor string, archive bool) error {
	tx, e := s.DB.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	// Admission, binding and archive must lock project before workspace, including audit FK locks.
	var project string
	if e = tx.QueryRow(ctx, "SELECT project_id FROM workspaces WHERE id=$1", id).Scan(&project); e != nil {
		return e
	}
	if e = tx.QueryRow(ctx, "SELECT id FROM projects WHERE id=$1 FOR UPDATE", project).Scan(&project); e != nil {
		return e
	}
	if e = executionLock(ctx, tx, id); e != nil {
		return e
	}
	w, e := scanWorkspace(tx.QueryRow(ctx, "SELECT "+workspaceCols+" FROM workspaces WHERE id=$1 FOR UPDATE", id))
	if e != nil {
		return e
	}
	var activeRun bool
	if e = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM run_contexts WHERE workspace_id=$1 AND released_at IS NULL)", id).Scan(&activeRun); e != nil {
		return e
	}
	if activeRun {
		return domain.Blocked("workspace_busy", "工作区已有主动执行，请结束后归档")
	}
	if w.ActiveSandboxID != "" {
		return ErrConflict
	}
	_, e = tx.Exec(ctx, `UPDATE workspaces SET archived_at=CASE WHEN $2 THEN COALESCE(archived_at,now()) ELSE NULL END,update_at=now(),update_by=$3 WHERE id=$1`, id, archive, actor)
	if e != nil {
		return e
	}
	action := "workspace.unarchive"
	if archive {
		action = "workspace.archive"
	}
	if e = resourceAudit(ctx, tx, w.ProjectID, w.ID, "", action, actor); e != nil {
		return e
	}
	return tx.Commit(ctx)
}
func (s *Store) ReleaseWorkspace(ctx context.Context, wid, sid string) error {
	_, e := s.DB.Exec(ctx, `UPDATE workspaces SET active_sandbox_id=NULL,update_at=now() WHERE id=$1 AND active_sandbox_id=$2`, wid, sid)
	return e
}
func (s *Store) WorkspaceTasks(ctx context.Context) ([]domain.Workspace, error) {
	rows, e := s.DB.Query(ctx, "SELECT "+workspaceCols+" FROM workspaces ORDER BY COALESCE(last_observed_at,'epoch') LIMIT 100")
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []domain.Workspace{}
	for rows.Next() {
		w, e := scanWorkspace(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

const conversationCols = `id,namespace_id,project_id,owner_id,integration_id,external_id,title,COALESCE(workspace_id,''),version`

func scanConversation(row pgx.Row) (domain.Conversation, error) {
	var c domain.Conversation
	e := row.Scan(&c.ID, &c.NamespaceID, &c.ProjectID, &c.OwnerID, &c.IntegrationID, &c.ExternalID, &c.Title, &c.WorkspaceID, &c.Version)
	return c, e
}
func (s *Store) Conversation(ctx context.Context, id string) (domain.Conversation, error) {
	return scanConversation(s.DB.QueryRow(ctx, "SELECT "+conversationCols+" FROM conversation_refs WHERE id=$1", id))
}
func (s *Store) Conversations(ctx context.Context, p, u string, all bool) ([]domain.Conversation, error) {
	rows, e := s.DB.Query(ctx, "SELECT "+conversationCols+" FROM conversation_refs WHERE project_id=$1 AND (owner_id=$2 OR $3) ORDER BY create_at DESC LIMIT 200", p, u, all)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []domain.Conversation{}
	for rows.Next() {
		c, e := scanConversation(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
func (s *Store) RegisterConversation(ctx context.Context, c domain.Conversation) (domain.Conversation, error) {
	n, e := s.Namespace(ctx, c.OwnerID)
	if e != nil {
		return c, e
	}
	return scanConversation(s.DB.QueryRow(ctx, `INSERT INTO conversation_refs(id,namespace_id,project_id,owner_id,integration_id,external_id,title,create_by,update_by) VALUES($1,$2,$3,$4,$5,$6,$7,$4,$4) ON CONFLICT(integration_id,project_id,owner_id,external_id) DO UPDATE SET external_id=excluded.external_id RETURNING `+conversationCols, ID(), n.ID, c.ProjectID, c.OwnerID, c.IntegrationID, c.ExternalID, c.Title))
}
func (s *Store) BindConversation(ctx context.Context, id, wid, actor string, expected int64) (domain.Conversation, error) {
	tx, e := s.DB.Begin(ctx)
	if e != nil {
		return domain.Conversation{}, e
	}
	defer tx.Rollback(ctx)
	var project string
	if e = tx.QueryRow(ctx, "SELECT project_id FROM conversation_refs WHERE id=$1", id).Scan(&project); e != nil {
		return domain.Conversation{}, e
	}
	if e = lockProject(ctx, tx, project); e != nil {
		return domain.Conversation{}, e
	}
	c, e := scanConversation(tx.QueryRow(ctx, "SELECT "+conversationCols+" FROM conversation_refs WHERE id=$1 FOR UPDATE", id))
	if e != nil {
		return c, e
	}
	if c.Version != expected {
		return c, ErrConflict
	}
	if c.WorkspaceID == wid {
		return c, tx.Commit(ctx)
	}
	for _, target := range []string{c.WorkspaceID, wid} {
		if target == "" {
			continue
		}
		if e = executionLock(ctx, tx, target); e != nil {
			return c, e
		}
		var activeRun bool
		if e = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM run_contexts WHERE workspace_id=$1 AND released_at IS NULL)", target).Scan(&activeRun); e != nil {
			return c, e
		}
		if activeRun {
			return c, domain.Blocked("workspace_busy", "相关工作区有主动执行，请先结束")
		}
		w, err := scanWorkspace(tx.QueryRow(ctx, "SELECT "+workspaceCols+" FROM workspaces WHERE id=$1 FOR UPDATE", target))
		if err != nil {
			return c, err
		}
		if w.ProjectID != c.ProjectID || w.OwnerID != c.OwnerID {
			return c, pgx.ErrNoRows
		}
		if w.ActiveSandboxID != "" || (target == wid && w.ArchivedAt != nil) {
			return c, ErrConflict
		}
	}
	_, e = tx.Exec(ctx, "UPDATE conversation_refs SET workspace_id=NULLIF($2,''),version=version+1,update_at=now(),update_by=$3 WHERE id=$1", id, wid, actor)
	if e != nil {
		return c, e
	}
	_, e = tx.Exec(ctx, "INSERT INTO conversation_binding_history(conversation_id,workspace_id,version,create_by,update_by) VALUES($1,NULLIF($2,''),$3,$4,$4)", id, wid, c.Version+1, actor)
	if e != nil {
		return c, e
	}
	if e = resourceAudit(ctx, tx, c.ProjectID, wid, id, "conversation.bind", actor); e != nil {
		return c, e
	}
	if e = tx.Commit(ctx); e != nil {
		return c, e
	}
	return s.Conversation(ctx, id)
}
func (s *Store) BindingHistory(ctx context.Context, id string) ([]domain.Binding, error) {
	rows, e := s.DB.Query(ctx, "SELECT COALESCE(workspace_id,''),version,create_by,create_at FROM conversation_binding_history WHERE conversation_id=$1 ORDER BY version DESC LIMIT 100", id)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []domain.Binding{}
	for rows.Next() {
		var b domain.Binding
		if e = rows.Scan(&b.WorkspaceID, &b.Version, &b.Actor, &b.CreateAt); e != nil {
			return nil, e
		}
		out = append(out, b)
	}
	return out, rows.Err()
}
