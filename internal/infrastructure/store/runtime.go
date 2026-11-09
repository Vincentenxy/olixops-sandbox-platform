package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"olixops-agent-platform/internal/domain"
)

var ErrLeaseLost = errors.New("preparation lease is no longer current")

const runCols = `r.id,r.project_id,r.owner_id,r.actor,r.conversation_id,r.workspace_id,r.binding_version,COALESCE(r.sandbox_id,''),r.attachment_generation,r.image,r.ttl,r.state,r.reason,r.message,r.released_at,r.create_at,o.state,o.reason,o.message,o.attempt,o.generation,o.lease_owner,o.lease_until,o.next_attempt_at,o.create_sent_at,o.owns_creation`

func scanRun(row pgx.Row) (domain.RunContext, error) {
	var r domain.RunContext
	e := row.Scan(&r.ID, &r.ProjectID, &r.OwnerID, &r.Actor, &r.ConversationID, &r.WorkspaceID, &r.BindingVersion, &r.SandboxID, &r.AttachmentGeneration, &r.Image, &r.TTL, &r.State, &r.Reason, &r.Message, &r.ReleasedAt, &r.CreateAt, &r.Operation.State, &r.Operation.Reason, &r.Operation.Message, &r.Operation.Attempt, &r.Operation.Generation, &r.Operation.LeaseOwner, &r.Operation.LeaseUntil, &r.Operation.NextAttemptAt, &r.Operation.CreateSentAt, &r.Operation.OwnsCreation)
	return r, e
}

const runFrom = ` FROM run_contexts r JOIN runtime_operations o ON o.id=r.id `

func (s *Store) Run(ctx context.Context, id string) (domain.RunContext, error) {
	return scanRun(s.DB.QueryRow(ctx, "SELECT "+runCols+runFrom+"WHERE r.id=$1", id))
}
func (s *Store) Runs(ctx context.Context, project, actor string) ([]domain.RunContext, error) {
	// Keep every active execution visible even after many newer historical runs.
	rows, e := s.DB.Query(ctx, "SELECT "+runCols+runFrom+`WHERE r.project_id=$1 AND r.actor=$2 AND (r.released_at IS NULL OR r.id IN (
 SELECT id FROM run_contexts WHERE project_id=$1 AND actor=$2 AND released_at IS NOT NULL ORDER BY create_at DESC,id LIMIT 100
 )) ORDER BY r.create_at DESC,r.id`, project, actor)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	result := []domain.RunContext{}
	for rows.Next() {
		r, e := scanRun(rows)
		if e != nil {
			return nil, e
		}
		result = append(result, r)
	}
	return result, rows.Err()
}

// All resource mutations use project -> conversation -> workspace lock order.
func lockProject(ctx context.Context, tx pgx.Tx, id string) error {
	var p string
	return tx.QueryRow(ctx, "SELECT id FROM projects WHERE id=$1 FOR UPDATE", id).Scan(&p)
}
func executionLock(ctx context.Context, tx pgx.Tx, wid string) error {
	if wid == "" {
		return nil
	}
	var acquired bool
	if e := tx.QueryRow(ctx, "SELECT pg_try_advisory_xact_lock(hashtextextended($1,6413))", wid).Scan(&acquired); e != nil {
		return e
	}
	if !acquired {
		return domain.Blocked("operation_in_progress", "工作区正在执行操作，请等待完成后重试")
	}
	return nil
}
func actorAccess(ctx context.Context, tx pgx.Tx, project, owner, actor string, admin bool) error {
	var enabled bool
	if e := tx.QueryRow(ctx, "SELECT NOT EXISTS(SELECT 1 FROM local_users WHERE user_id=$1 AND NOT enabled)", actor).Scan(&enabled); e != nil {
		return e
	}
	if !enabled {
		return pgx.ErrNoRows
	}
	if admin {
		return nil
	}
	var role string
	if e := tx.QueryRow(ctx, "SELECT role FROM project_members WHERE project_id=$1 AND user_id=$2", project, actor).Scan(&role); e != nil {
		return e
	}
	if !domain.CanAccess(owner, actor, role, false) {
		return pgx.ErrNoRows
	}
	return nil
}

func (s *Store) BeginRun(ctx context.Context, q domain.ContinueRequest, actor string, admin bool) (domain.RunContext, error) {
	tx, e := s.DB.Begin(ctx)
	if e != nil {
		return domain.RunContext{}, e
	}
	defer tx.Rollback(ctx)
	var project string
	if e = tx.QueryRow(ctx, "SELECT project_id FROM conversation_refs WHERE id=$1", q.ConversationID).Scan(&project); e != nil {
		return domain.RunContext{}, e
	}
	if e = lockProject(ctx, tx, project); e != nil {
		return domain.RunContext{}, e
	}
	c, e := scanConversation(tx.QueryRow(ctx, "SELECT "+conversationCols+" FROM conversation_refs WHERE id=$1 FOR UPDATE", q.ConversationID))
	if e != nil {
		return domain.RunContext{}, e
	}
	if e = actorAccess(ctx, tx, project, c.OwnerID, actor, admin); e != nil {
		return domain.RunContext{}, e
	}
	old, e := scanRun(tx.QueryRow(ctx, "SELECT "+runCols+runFrom+"JOIN run_requests k ON k.run_id=r.id WHERE k.project_id=$1 AND k.actor=$2 AND k.request_key=$3", project, actor, q.RequestKey))
	if e == nil {
		if old.ConversationID != c.ID || old.BindingVersion != *q.ExpectedVersion || old.Image != q.Image || old.TTL != q.TTL {
			return old, ErrConflict
		}
		return old, tx.Commit(ctx)
	}
	if !errors.Is(e, pgx.ErrNoRows) {
		return old, e
	}
	if c.Version != *q.ExpectedVersion {
		return old, domain.Blocked("binding_changed", "对话绑定已变化，请刷新后重新继续")
	}
	if c.WorkspaceID == "" {
		return old, domain.Blocked("workspace_required", "请先为对话绑定一个工作区")
	}
	if e = executionLock(ctx, tx, c.WorkspaceID); e != nil {
		return old, e
	}
	w, e := scanWorkspace(tx.QueryRow(ctx, "SELECT "+workspaceCols+" FROM workspaces WHERE id=$1 FOR UPDATE", c.WorkspaceID))
	if e != nil {
		return old, e
	}
	if w.OwnerID != c.OwnerID || w.ProjectID != project {
		return old, pgx.ErrNoRows
	}
	if w.ArchivedAt != nil {
		return old, domain.Blocked("workspace_archived", "请先取消工作区归档")
	}
	current, e := scanRun(tx.QueryRow(ctx, "SELECT "+runCols+runFrom+"WHERE r.workspace_id=$1 AND r.released_at IS NULL", w.ID))
	if e == nil {
		if current.Actor != actor || current.ConversationID != c.ID || current.BindingVersion != c.Version {
			return old, domain.Blocked("workspace_busy", "工作区已有其他主动执行上下文，请先结束该执行")
		}
		if current.Image != q.Image || current.TTL != q.TTL {
			return old, domain.Blocked("runtime_settings_changed", "当前执行已使用其他运行配置，请结束后重新继续")
		}
	} else if !errors.Is(e, pgx.ErrNoRows) {
		return old, e
	} else {
		id := ID()
		_, e = tx.Exec(ctx, `INSERT INTO run_contexts(id,project_id,owner_id,actor,conversation_id,workspace_id,binding_version,image,ttl,create_by,update_by,message) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$4,$4,'运行准备已登记')`, id, project, c.OwnerID, actor, c.ID, w.ID, c.Version, q.Image, q.TTL)
		if e != nil {
			return old, e
		}
		if _, e = tx.Exec(ctx, `INSERT INTO runtime_operations(id,create_by,update_by,message) VALUES($1,$2,$2,'等待后台准备')`, id, actor); e != nil {
			return old, e
		}
		current, e = scanRun(tx.QueryRow(ctx, "SELECT "+runCols+runFrom+"WHERE r.id=$1", id))
		if e != nil {
			return old, e
		}
		if e = runAudit(ctx, tx, current, "run.continue", "requested", actor); e != nil {
			return old, e
		}
	}
	_, e = tx.Exec(ctx, `INSERT INTO run_requests(project_id,actor,request_key,run_id,create_by,update_by) VALUES($1,$2,$3,$4,$2,$2)`, project, actor, q.RequestKey, current.ID)
	if e != nil {
		return current, e
	}
	return current, tx.Commit(ctx)
}
func runAudit(ctx context.Context, tx pgx.Tx, r domain.RunContext, action, outcome, actor string) error {
	_, e := tx.Exec(ctx, `INSERT INTO audit_events(project_id,sandbox_id,workspace_id,conversation_id,run_context_id,action,actor,outcome,create_by,update_by) SELECT project_id,COALESCE(sandbox_id,''),workspace_id,conversation_id,id,$2,$3,$4,$3,$3 FROM run_contexts WHERE id=$1`, r.ID, action, actor, outcome)
	return e
}

// A lease claim commits before any external I/O. A new claimant increments generation.
func (s *Store) ClaimPreparation(ctx context.Context, worker string) (domain.RunContext, error) {
	var id string
	var generation int64
	e := s.DB.QueryRow(ctx, `WITH candidate AS (
 SELECT id FROM runtime_operations WHERE state IN ('Pending','Preparing','Waiting','Ready') AND next_attempt_at<=now() AND (lease_until IS NULL OR lease_until<now()) ORDER BY next_attempt_at,id FOR UPDATE SKIP LOCKED LIMIT 1
 ) UPDATE runtime_operations o SET lease_owner=$1,lease_until=now()+interval '75 seconds',generation=generation+1,attempt=attempt+1,state=CASE WHEN o.state='Pending' THEN 'Preparing' ELSE o.state END,update_at=now() FROM candidate c WHERE o.id=c.id RETURNING o.id,o.generation`, worker).Scan(&id, &generation)
	if e != nil {
		return domain.RunContext{}, e
	}
	r, e := s.Run(ctx, id)
	if e == nil && (r.Operation.Generation != generation || r.Operation.LeaseOwner != worker) {
		return r, ErrLeaseLost
	}
	return r, e
}
func claimCurrent(ctx context.Context, tx pgx.Tx, r domain.RunContext) error {
	var id string
	e := tx.QueryRow(ctx, `SELECT id FROM runtime_operations WHERE id=$1 AND generation=$2 AND lease_owner=$3 AND lease_until>clock_timestamp() AND state NOT IN ('Failed','Cancelled') FOR UPDATE`, r.ID, r.Operation.Generation, r.Operation.LeaseOwner).Scan(&id)
	if errors.Is(e, pgx.ErrNoRows) {
		return ErrLeaseLost
	}
	return e
}

// Reserve the sandbox and pin attachment identity atomically. No remote create occurs here.
func (s *Store) ReserveRunSandbox(ctx context.Context, r domain.RunContext, admin bool) (domain.Sandbox, error) {
	tx, e := s.DB.Begin(ctx)
	if e != nil {
		return domain.Sandbox{}, e
	}
	defer tx.Rollback(ctx)
	if e = lockProject(ctx, tx, r.ProjectID); e != nil {
		return domain.Sandbox{}, e
	}
	c, e := scanConversation(tx.QueryRow(ctx, "SELECT "+conversationCols+" FROM conversation_refs WHERE id=$1 FOR UPDATE", r.ConversationID))
	if e != nil {
		return domain.Sandbox{}, e
	}
	if e = actorAccess(ctx, tx, r.ProjectID, r.OwnerID, r.Actor, admin); e != nil {
		return domain.Sandbox{}, e
	}
	if e = executionLock(ctx, tx, r.WorkspaceID); e != nil {
		return domain.Sandbox{}, e
	}
	w, e := scanWorkspace(tx.QueryRow(ctx, "SELECT "+workspaceCols+" FROM workspaces WHERE id=$1 FOR UPDATE", r.WorkspaceID))
	if e != nil {
		return domain.Sandbox{}, e
	}
	if e = claimCurrent(ctx, tx, r); e != nil {
		return domain.Sandbox{}, e
	}
	current, e := scanRun(tx.QueryRow(ctx, "SELECT "+runCols+runFrom+"WHERE r.id=$1", r.ID))
	if e != nil {
		return domain.Sandbox{}, e
	}
	if current.ReleasedAt != nil || c.Version != r.BindingVersion || c.WorkspaceID != r.WorkspaceID {
		return domain.Sandbox{}, domain.Blocked("binding_changed", "对话绑定已变化，本次准备已失效")
	}
	if w.ArchivedAt != nil {
		return domain.Sandbox{}, domain.Blocked("workspace_archived", "工作区已归档")
	}
	if current.SandboxID != "" {
		b, e := scan(tx.QueryRow(ctx, "SELECT "+cols+" FROM sandboxes WHERE id=$1", current.SandboxID))
		if e != nil {
			return b, e
		}
		return b, tx.Commit(ctx)
	}
	var b domain.Sandbox
	fresh := false
	if w.ActiveSandboxID != "" {
		b, e = scan(tx.QueryRow(ctx, "SELECT "+cols+" FROM sandboxes WHERE id=$1", w.ActiveSandboxID))
		if e != nil {
			return b, e
		}
		if b.Image != r.Image {
			return b, domain.Blocked("image_mismatch", "已有实例镜像不同，请停止旧实例后再继续")
		}
	} else {
		b, e = reserveNewSandbox(ctx, tx, domain.Sandbox{ProjectID: r.ProjectID, OwnerID: r.OwnerID, WorkspaceID: r.WorkspaceID, Name: "conversation-" + r.ID[:12], Image: r.Image, TTL: r.TTL, RequestKey: "run-" + r.ID}, r.Actor, r.ID)
		if e != nil {
			return b, e
		}
		fresh = true
		w.AttachmentGeneration++
	}
	_, e = tx.Exec(ctx, "UPDATE run_contexts SET sandbox_id=$2,attachment_generation=$3,update_at=now() WHERE id=$1", r.ID, b.ID, w.AttachmentGeneration)
	if e != nil {
		return b, e
	}
	_, e = tx.Exec(ctx, "UPDATE runtime_operations SET owns_creation=$2 WHERE id=$1", r.ID, fresh)
	if e != nil {
		return b, e
	}
	return b, tx.Commit(ctx)
}

func (s *Store) MarkCreateSent(ctx context.Context, r domain.RunContext) (bool, error) {
	tx, e := s.DB.Begin(ctx)
	if e != nil {
		return false, e
	}
	defer tx.Rollback(ctx)
	if e = lockProject(ctx, tx, r.ProjectID); e != nil {
		return false, e
	}
	if e = claimCurrent(ctx, tx, r); e != nil {
		return false, e
	}
	tag, e := tx.Exec(ctx, `UPDATE runtime_operations SET create_sent_at=now() WHERE id=$1 AND owns_creation AND create_sent_at IS NULL`, r.ID)
	if e != nil {
		return false, e
	}
	if tag.RowsAffected() == 0 {
		return false, tx.Commit(ctx)
	}
	_, e = tx.Exec(ctx, `UPDATE sandboxes SET state='Unknown',update_at=now() WHERE id=(SELECT sandbox_id FROM run_contexts WHERE id=$1) AND backend_id='' AND state='Creating'`, r.ID)
	if e != nil {
		return false, e
	}
	return true, tx.Commit(ctx)
}
func (s *Store) SavePreparedSandbox(ctx context.Context, r domain.RunContext, backend, state string, expires *time.Time) error {
	tx, e := s.DB.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	if e = lockProject(ctx, tx, r.ProjectID); e != nil {
		return e
	}
	if e = claimCurrent(ctx, tx, r); e != nil {
		return e
	}
	_, e = tx.Exec(ctx, `UPDATE sandboxes SET backend_id=CASE WHEN $2='' THEN backend_id ELSE $2 END,state=CASE WHEN state IN ('Deleted','Expired','Failed','Deleting') THEN state ELSE $3 END,expires_at=COALESCE($4,expires_at),update_at=now() WHERE id=(SELECT sandbox_id FROM run_contexts WHERE id=$1)`, r.ID, backend, state, expires)
	if e != nil {
		return e
	}
	return tx.Commit(ctx)
}
func (s *Store) FinishPreparation(ctx context.Context, r domain.RunContext, state, reason, message string) error {
	tx, e := s.DB.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	if e = lockProject(ctx, tx, r.ProjectID); e != nil {
		return e
	}
	if e = claimCurrent(ctx, tx, r); e != nil {
		return e
	}
	if state == "Ready" {
		var valid bool
		e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM run_contexts r JOIN conversation_refs c ON c.id=r.conversation_id JOIN workspaces w ON w.id=r.workspace_id JOIN sandboxes b ON b.id=r.sandbox_id WHERE r.id=$1 AND r.released_at IS NULL AND c.version=r.binding_version AND c.workspace_id=r.workspace_id AND w.active_sandbox_id=r.sandbox_id AND w.attachment_generation=r.attachment_generation AND w.archived_at IS NULL AND w.state='Available' AND w.storage_phase<>'Unknown' AND b.state='Running' AND (b.expires_at IS NULL OR b.expires_at>now()))`, r.ID).Scan(&valid)
		if e != nil {
			return e
		}
		if !valid {
			state, reason, message = "Waiting", "runtime_changed", "运行状态已变化，等待重新核对"
		}
	}
	terminal := state == "Failed" || state == "Invalidated"
	opState := state
	if terminal {
		opState = "Failed"
	}
	tag, e := tx.Exec(ctx, `UPDATE run_contexts SET state=$2,reason=$3,message=$4,released_at=CASE WHEN $5 THEN now() ELSE NULL END,update_at=now() WHERE id=$1 AND released_at IS NULL`, r.ID, state, reason, message, terminal)
	if e != nil {
		return e
	}
	if tag.RowsAffected() != 1 {
		return ErrLeaseLost
	}
	_, e = tx.Exec(ctx, `UPDATE runtime_operations SET state=$2,reason=$3,message=$4,lease_until=NULL,lease_owner='',next_attempt_at=now()+interval '10 seconds',update_at=now() WHERE id=$1`, r.ID, opState, reason, message)
	if e != nil {
		return e
	}
	if (state == "Ready" && r.State != "Ready") || terminal {
		if e = runAudit(ctx, tx, r, "run.prepare", state, r.Actor); e != nil {
			return e
		}
	}
	return tx.Commit(ctx)
}
func (s *Store) ReleaseRun(ctx context.Context, r domain.RunContext, actor string) error {
	tx, e := s.DB.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	if e = lockProject(ctx, tx, r.ProjectID); e != nil {
		return e
	}
	if e = executionLock(ctx, tx, r.WorkspaceID); e != nil {
		return e
	}
	current, e := scanRun(tx.QueryRow(ctx, "SELECT "+runCols+runFrom+"WHERE r.id=$1", r.ID))
	if e != nil {
		return e
	}
	if current.Actor != actor {
		return pgx.ErrNoRows
	}
	if current.ReleasedAt != nil {
		return nil
	}
	if current.State != "Ready" {
		return domain.Blocked("preparation_pending", "准备结果尚未确定，请等待核对；不能释放未知运行")
	}
	if _, e = tx.Exec(ctx, `UPDATE run_contexts SET state='Released',released_at=now(),update_at=now(),update_by=$2,message='本次执行已结束；后台进程和工作区文件仍保留' WHERE id=$1`, r.ID, actor); e != nil {
		return e
	}
	if _, e = tx.Exec(ctx, `UPDATE runtime_operations SET state='Cancelled',generation=generation+1,lease_owner='',lease_until=NULL,update_at=now(),update_by=$2 WHERE id=$1`, r.ID, actor); e != nil {
		return e
	}
	if e = runAudit(ctx, tx, r, "run.release", "success", actor); e != nil {
		return e
	}
	return tx.Commit(ctx)
}

// Keep the advisory transaction alive through the HTTP operation. No workspace row lock is held.
// Every admission/binding/release uses the same non-blocking lock before changing execution targets.
func (s *Store) GuardSandbox(ctx context.Context, b domain.Sandbox, runID, actor string, dataOperation bool) (func(), error) {
	tx, e := s.DB.Begin(ctx)
	if e != nil {
		return nil, e
	}
	release := func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = tx.Rollback(cleanup)
	}
	fail := func(e error) (func(), error) { release(); return nil, e }
	if e = executionLock(ctx, tx, b.WorkspaceID); e != nil {
		return fail(e)
	}
	if runID != "" {
		r, e := scanRun(tx.QueryRow(ctx, "SELECT "+runCols+runFrom+"WHERE r.id=$1", runID))
		if e != nil {
			return fail(e)
		}
		if r.Actor != actor || r.SandboxID != b.ID || r.WorkspaceID != b.WorkspaceID {
			return fail(pgx.ErrNoRows)
		}
		var valid bool
		e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM run_contexts r JOIN conversation_refs c ON c.id=r.conversation_id JOIN workspaces w ON w.id=r.workspace_id JOIN sandboxes b ON b.id=r.sandbox_id WHERE r.id=$1 AND r.released_at IS NULL AND r.state='Ready' AND c.version=r.binding_version AND c.workspace_id=r.workspace_id AND w.active_sandbox_id=r.sandbox_id AND w.attachment_generation=r.attachment_generation AND w.archived_at IS NULL AND w.state='Available' AND w.storage_phase<>'Unknown' AND b.state='Running' AND (b.expires_at IS NULL OR b.expires_at>now()))`, runID).Scan(&valid)
		if e != nil {
			return fail(e)
		}
		if !valid {
			return fail(domain.Blocked("run_context_stale", "执行上下文已失效，请回到对话重新继续"))
		}
	} else if b.WorkspaceID != "" && dataOperation {
		var active bool
		if e = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM run_contexts WHERE workspace_id=$1 AND released_at IS NULL)", b.WorkspaceID).Scan(&active); e != nil {
			return fail(e)
		}
		if active {
			return fail(domain.Blocked("run_context_required", "该工作区已有主动执行，请从对应对话进入实例"))
		}
	}
	return release, nil
}
func (s *Store) AuditRunAction(ctx context.Context, b domain.Sandbox, runID, action, actor, outcome string) error {
	if runID == "" {
		return s.Audit(ctx, b.ProjectID, b.ID, action, actor, outcome)
	}
	_, e := s.DB.Exec(ctx, `INSERT INTO audit_events(project_id,sandbox_id,workspace_id,conversation_id,run_context_id,action,actor,outcome,create_by,update_by) SELECT project_id,sandbox_id,workspace_id,conversation_id,id,$2,$3,$4,$3,$3 FROM run_contexts WHERE id=$1`, runID, action, actor, outcome)
	return e
}

func (s *Store) RunActorAllowed(ctx context.Context, r domain.RunContext, admin bool) error {
	tx, e := s.DB.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	return actorAccess(ctx, tx, r.ProjectID, r.OwnerID, r.Actor, admin)
}
