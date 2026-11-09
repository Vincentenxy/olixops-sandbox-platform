package store

import (
	"context"
	"crypto/rand"
	_ "embed"
	"encoding/hex"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"olixops-sandbox-platform/internal/domain"
)

//go:embed schema.sql
var schema string

//go:embed auth_schema.sql
var authSchema string

//go:embed workspace_schema.sql
var workspaceSchema string

//go:embed runtime_schema.sql
var runtimeSchema string
var ErrConflict = errors.New("idempotency key reused with different parameters")
var ErrQuota = errors.New("project sandbox quota exceeded")
var ErrNotFound = errors.New("not found")

type Store struct{ DB *pgxpool.Pool }

func ID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}
func Open(ctx context.Context, dsn string, migrate bool) (*Store, error) {
	p, e := pgxpool.New(ctx, dsn)
	if e != nil {
		return nil, e
	}
	if e = p.Ping(ctx); e != nil {
		p.Close()
		return nil, e
	}
	if migrate {
		_, e = p.Exec(ctx, schema+"\n"+authSchema+"\n"+workspaceSchema+"\n"+runtimeSchema)
		if e != nil {
			p.Close()
			return nil, e
		}
	}
	return &Store{p}, nil
}
func (s *Store) Role(ctx context.Context, p, u string) (string, error) {
	var r string
	e := s.DB.QueryRow(ctx, "SELECT role FROM project_members WHERE project_id=$1 AND user_id=$2", p, u).Scan(&r)
	return r, e
}
func (s *Store) Projects(ctx context.Context, u string, admin bool) ([]domain.Project, error) {
	rows, e := s.DB.Query(ctx, `SELECT p.id,p.name,COALESCE(m.role,'admin'),p.quota,p.create_at FROM projects p LEFT JOIN project_members m ON p.id=m.project_id AND m.user_id=$1 WHERE m.user_id IS NOT NULL OR $2 ORDER BY p.create_at DESC`, u, admin)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []domain.Project{}
	for rows.Next() {
		var p domain.Project
		if e = rows.Scan(&p.ID, &p.Name, &p.Role, &p.Quota, &p.CreateAt); e != nil {
			return nil, e
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
func (s *Store) CreateProject(ctx context.Context, u, name string, quota int) (string, error) {
	tx, e := s.DB.Begin(ctx)
	if e != nil {
		return "", e
	}
	defer tx.Rollback(ctx)
	id := ID()
	_, e = tx.Exec(ctx, `INSERT INTO projects(id,name,quota,create_by,update_by) VALUES($1,$2,$3,$4,$4)`, id, name, quota, u)
	if e != nil {
		return "", e
	}
	_, e = tx.Exec(ctx, `INSERT INTO project_members(project_id,user_id,role,create_by,update_by) VALUES($1,$2,'owner',$2,$2)`, id, u)
	if e != nil {
		return "", e
	}
	_, e = tx.Exec(ctx, `INSERT INTO audit_events(project_id,action,actor,outcome,create_by,update_by) VALUES($1,'project.create',$2,'success',$2,$2)`, id, u)
	if e != nil {
		return "", e
	}
	return id, tx.Commit(ctx)
}
func (s *Store) Members(ctx context.Context, p string) ([]domain.Member, error) {
	rows, e := s.DB.Query(ctx, "SELECT user_id,role FROM project_members WHERE project_id=$1 ORDER BY role,user_id", p)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []domain.Member{}
	for rows.Next() {
		var m domain.Member
		if e = rows.Scan(&m.UserID, &m.Role); e != nil {
			return nil, e
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
func (s *Store) SetMember(ctx context.Context, p, u, role, actor string) error {
	tx, e := s.DB.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	tag, e := tx.Exec(ctx, `INSERT INTO project_members(project_id,user_id,role,create_by,update_by) VALUES($1,$2,$3,$4,$4) ON CONFLICT(project_id,user_id) DO UPDATE SET role=$3,update_by=$4,update_at=now() WHERE project_members.role <> 'owner'`, p, u, role, actor)
	if e != nil {
		return e
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	_, e = tx.Exec(ctx, `INSERT INTO audit_events(project_id,action,actor,outcome,create_by,update_by) VALUES($1,'member.set',$2,'success',$2,$2)`, p, actor)
	if e != nil {
		return e
	}
	return tx.Commit(ctx)
}

const cols = `id,project_id,owner_id,name,image,backend_id,state,request_key,ttl,create_at,update_at,expires_at,COALESCE(workspace_id,'')`

func scan(row pgx.Row) (domain.Sandbox, error) {
	var b domain.Sandbox
	e := row.Scan(&b.ID, &b.ProjectID, &b.OwnerID, &b.Name, &b.Image, &b.BackendID, &b.State, &b.RequestKey, &b.TTL, &b.CreateAt, &b.UpdateAt, &b.ExpiresAt, &b.WorkspaceID)
	return b, e
}
func (s *Store) Get(ctx context.Context, id string) (domain.Sandbox, error) {
	return scan(s.DB.QueryRow(ctx, "SELECT "+cols+" FROM sandboxes WHERE id=$1", id))
}
func (s *Store) List(ctx context.Context, p, u string, all bool) ([]domain.Sandbox, error) {
	rows, e := s.DB.Query(ctx, "SELECT "+cols+` FROM sandboxes WHERE project_id=$1 AND (owner_id=$2 OR $3)
 AND (state NOT IN ('Deleted','Expired','Failed') OR id IN (
 SELECT id FROM sandboxes WHERE project_id=$1 AND (owner_id=$2 OR $3) AND state IN ('Deleted','Expired','Failed') ORDER BY create_at DESC,id LIMIT 200
 )) ORDER BY create_at DESC,id`, p, u, all)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []domain.Sandbox{}
	for rows.Next() {
		b, e := scan(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// Reserve serializes admission per project. Idempotency is scoped to the authenticated owner.
func (s *Store) Reserve(ctx context.Context, b domain.Sandbox, actor string) (domain.Sandbox, bool, error) {
	tx, e := s.DB.Begin(ctx)
	if e != nil {
		return b, false, e
	}
	defer tx.Rollback(ctx)
	var quota int
	e = tx.QueryRow(ctx, "SELECT quota FROM projects WHERE id=$1 FOR UPDATE", b.ProjectID).Scan(&quota)
	if e != nil {
		return b, false, e
	}
	old, e := scan(tx.QueryRow(ctx, "SELECT "+cols+" FROM sandboxes WHERE project_id=$1 AND owner_id=$2 AND request_key=$3", b.ProjectID, b.OwnerID, b.RequestKey))
	if e == nil {
		if old.Name != b.Name || old.Image != b.Image || old.TTL != b.TTL || old.WorkspaceID != b.WorkspaceID {
			return old, false, ErrConflict
		}
		return old, false, tx.Commit(ctx)
	}
	if !errors.Is(e, pgx.ErrNoRows) {
		return b, false, e
	}
	b, e = reserveNewSandbox(ctx, tx, b, actor, "")
	if e != nil {
		return b, false, e
	}
	e = tx.Commit(ctx)
	if e != nil {
		return b, false, e
	}
	b, e = s.Get(ctx, b.ID)
	return b, true, e
}

// Caller holds the project lock. This helper atomically admits one compute consumer.
func reserveNewSandbox(ctx context.Context, tx pgx.Tx, b domain.Sandbox, actor, runID string) (domain.Sandbox, error) {
	var quota int
	e := tx.QueryRow(ctx, "SELECT quota FROM projects WHERE id=$1", b.ProjectID).Scan(&quota)
	if e != nil {
		return b, e
	}
	if b.WorkspaceID != "" {
		if e = executionLock(ctx, tx, b.WorkspaceID); e != nil {
			return b, e
		}
		var occupied bool
		if e = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM run_contexts WHERE workspace_id=$1 AND released_at IS NULL AND id<>$2)", b.WorkspaceID, runID).Scan(&occupied); e != nil {
			return b, e
		}
		if occupied {
			return b, domain.Blocked("workspace_busy", "工作区已有主动执行，请从对话继续")
		}
		w, err := scanWorkspace(tx.QueryRow(ctx, "SELECT "+workspaceCols+" FROM workspaces WHERE id=$1 FOR UPDATE", b.WorkspaceID))
		if err != nil {
			return b, err
		}
		if w.ProjectID != b.ProjectID || w.OwnerID != b.OwnerID {
			return b, pgx.ErrNoRows
		}
		if w.State != "Available" || w.ArchivedAt != nil || w.ActiveSandboxID != "" || w.StoragePhase == "Unknown" {
			return b, ErrConflict
		}
	}
	var n int
	e = tx.QueryRow(ctx, `SELECT count(*) FROM sandboxes WHERE project_id=$1 AND state NOT IN ('Deleted','Failed','Expired')`, b.ProjectID).Scan(&n)
	if e != nil {
		return b, e
	}
	if n >= quota {
		return b, ErrQuota
	}
	b.ID = ID()
	_, e = tx.Exec(ctx, `INSERT INTO sandboxes(id,project_id,owner_id,name,image,state,request_key,ttl,create_by,update_by,workspace_id) VALUES($1,$2,$3,$4,$5,'Creating',$6,$7,$9,$9,NULLIF($8,''))`, b.ID, b.ProjectID, b.OwnerID, b.Name, b.Image, b.RequestKey, b.TTL, b.WorkspaceID, actor)
	if e != nil {
		return b, e
	}
	if b.WorkspaceID != "" {
		_, e = tx.Exec(ctx, "UPDATE workspaces SET active_sandbox_id=$2,attachment_generation=attachment_generation+1,update_at=now() WHERE id=$1", b.WorkspaceID, b.ID)
		if e != nil {
			return b, e
		}
	}
	_, e = tx.Exec(ctx, `INSERT INTO audit_events(project_id,sandbox_id,action,actor,outcome,create_by,update_by) VALUES($1,$2,'sandbox.create',$3,'requested',$3,$3)`, b.ProjectID, b.ID, actor)
	if e != nil {
		return b, e
	}
	b.State = "Creating"
	return b, nil
}
func (s *Store) Update(ctx context.Context, id, backend, state string, expires any) error {
	_, e := s.DB.Exec(ctx, `UPDATE sandboxes SET backend_id=CASE WHEN $2='' THEN backend_id ELSE $2 END,state=CASE WHEN state IN ('Deleted','Expired','Failed') THEN state WHEN state='Deleting' AND $3 NOT IN ('Deleted','Deleting') THEN state ELSE $3 END,expires_at=COALESCE($4,expires_at),update_at=now() WHERE id=$1`, id, backend, state, expires)
	return e
}
func (s *Store) Audit(ctx context.Context, p, id, action, actor, outcome string) error {
	_, e := s.DB.Exec(ctx, `INSERT INTO audit_events(project_id,sandbox_id,action,actor,outcome,create_by,update_by) VALUES($1,$2,$3,$4,$5,$4,$4)`, p, id, action, actor, outcome)
	return e
}
func (s *Store) Audits(ctx context.Context, p, u string, all bool) ([]domain.Audit, error) {
	rows, e := s.DB.Query(ctx, `SELECT id,project_id,sandbox_id,action,actor,outcome,create_at,workspace_id,conversation_id,run_context_id FROM audit_events WHERE project_id=$1 AND (actor=$2 OR $3) ORDER BY id DESC LIMIT 200`, p, u, all)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []domain.Audit{}
	for rows.Next() {
		var a domain.Audit
		if e = rows.Scan(&a.ID, &a.ProjectID, &a.SandboxID, &a.Action, &a.Actor, &a.Outcome, &a.CreateAt, &a.WorkspaceID, &a.ConversationID, &a.RunContextID); e != nil {
			return nil, e
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
func (s *Store) Active(ctx context.Context) ([]domain.Sandbox, error) {
	rows, e := s.DB.Query(ctx, "SELECT "+cols+" FROM sandboxes WHERE state NOT IN ('Deleted','Failed','Expired') ORDER BY update_at LIMIT 200")
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []domain.Sandbox{}
	for rows.Next() {
		b, e := scan(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, b)
	}
	return out, rows.Err()
}
