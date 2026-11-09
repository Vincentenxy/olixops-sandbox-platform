package router_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"olixops-sandbox-platform/internal/application"
	"olixops-sandbox-platform/internal/domain"
	"olixops-sandbox-platform/internal/infrastructure/config"
	"olixops-sandbox-platform/internal/infrastructure/sandbox"
	"olixops-sandbox-platform/internal/infrastructure/store"
	"olixops-sandbox-platform/internal/interfaces/router"
	"olixops-sandbox-platform/pkg/logger"
)

type runtimeBackend struct {
	mu        sync.Mutex
	creates   int
	dataCalls int
	mode      string
	resources map[string]map[string]any
	claims    []string
	files     map[string]string
}

func (b *runtimeBackend) serve(w http.ResponseWriter, r *http.Request) {
	b.mu.Lock()
	defer b.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	switch {
	case r.Method == "POST" && r.URL.Path == "/v1/sandboxes":
		b.creates++
		var q struct {
			Metadata map[string]string `json:"metadata"`
			Volumes  []struct {
				PVC struct {
					ClaimName string `json:"claimName"`
				} `json:"pvc"`
			} `json:"volumes"`
		}
		_ = json.NewDecoder(r.Body).Decode(&q)
		if len(q.Volumes) != 1 {
			w.WriteHeader(400)
			return
		}
		b.claims = append(b.claims, q.Volumes[0].PVC.ClaimName)
		id := fmt.Sprintf("remote-%d", b.creates)
		info := map[string]any{"id": id, "status": map[string]string{"state": "Running"}, "metadata": q.Metadata}
		if b.mode != "empty" {
			b.resources[id] = info
		}
		if b.mode == "empty" || b.mode == "lost" {
			w.WriteHeader(503)
			fmt.Fprint(w, `{"message":"lost response"}`)
			return
		}
		w.WriteHeader(202)
		_ = json.NewEncoder(w).Encode(info)
	case strings.Contains(r.URL.Path, "/endpoints/"):
		fmt.Fprint(w, `{"endpoint":"ignored"}`)
	case r.URL.Path == "/v1/sandboxes":
		items := []map[string]any{}
		for _, info := range b.resources {
			items = append(items, info)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"items": items, "pagination": map[string]int{"totalItems": len(items)}})
	case strings.HasPrefix(r.URL.Path, "/v1/sandboxes/"):
		id := strings.TrimPrefix(r.URL.Path, "/v1/sandboxes/")
		if r.Method == "DELETE" {
			delete(b.resources, id)
			w.WriteHeader(204)
			return
		}
		info, ok := b.resources[id]
		if !ok {
			w.WriteHeader(404)
			fmt.Fprint(w, `{"message":"gone"}`)
			return
		}
		_ = json.NewEncoder(w).Encode(info)
	case strings.HasSuffix(r.URL.Path, "/ping"):
		fmt.Fprint(w, `{"status":"ok"}`)
	case strings.HasSuffix(r.URL.Path, "/command"):
		b.dataCalls++
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "event: stdout\ndata: hello\n\n")
	case strings.HasSuffix(r.URL.Path, "/files/upload"):
		b.dataCalls++
		_ = r.ParseMultipartForm(5 << 20)
		fmt.Fprint(w, `{}`)
	default:
		b.dataCalls++
		fmt.Fprint(w, `{}`)
	}
}

type runtimeFixture struct {
	t            *testing.T
	s            *store.Store
	p            *application.Platform
	handler      http.Handler
	key          *rsa.PrivateKey
	backend      *runtimeBackend
	volumes      *fakeVolumes
	project      string
	workspace    domain.Workspace
	conversation domain.Conversation
}

func newRuntimeFixture(t *testing.T) *runtimeFixture {
	t.Helper()
	logger.Init("test")
	s := db(t)
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	pub, _ := x509.MarshalPKIXPublicKey(&key.PublicKey)
	cfg, e := config.Load("../../../configs")
	if e != nil {
		t.Fatal(e)
	}
	cfg.Auth.Local.Enabled = false
	cfg.Auth.Admins = nil
	cfg.Auth.JwtPublicKey = string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pub}))
	cfg.Server.Mode = "test"
	backend := &runtimeBackend{resources: map[string]map[string]any{}, files: map[string]string{}}
	up := httptest.NewServer(http.HandlerFunc(backend.serve))
	t.Cleanup(up.Close)
	volumes := &fakeVolumes{claims: map[string]string{}}
	p := &application.Platform{Store: s, Backend: sandbox.New(up.URL, ""), Storage: volumes}
	handler, e := router.New(cfg, p)
	if e != nil {
		t.Fatal(e)
	}
	f := &runtimeFixture{t: t, s: s, p: p, handler: handler, key: key, backend: backend, volumes: volumes}
	var project struct{ ID string }
	f.decode(f.call("alice", "project/create", map[string]any{"name": "runtime phase two"}, 200), &project)
	f.project = project.ID
	f.call("alice", "project/member/set", map[string]any{"projectId": f.project, "userId": "bob", "role": "member"}, 200)
	f.decode(f.call("alice", "workspace/create", map[string]any{"projectId": f.project, "name": "data", "requestKey": "workspace-phase2"}, 200), &f.workspace)
	f.decode(f.call("alice", "conversation/register", map[string]any{"projectId": f.project, "integrationId": "test", "externalId": "thread-1", "title": "app"}, 200), &f.conversation)
	f.decode(f.call("alice", "conversation/workspace", map[string]any{"id": f.conversation.ID, "workspaceId": f.workspace.ID, "expectedVersion": 0}, 200), &f.conversation)
	return f
}
func (f *runtimeFixture) decode(raw []byte, v any) {
	f.t.Helper()
	if e := json.Unmarshal(raw, v); e != nil {
		f.t.Fatal(e)
	}
}
func (f *runtimeFixture) call(user, route string, body any, status int) json.RawMessage {
	f.t.Helper()
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", "/api/v1/"+route, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	token, _ := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{"staffuserid": user, "iss": "olixops-local", "aud": "olixops-agent-platform", "exp": time.Now().Add(time.Hour).Unix()}).SignedString(f.key)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)
	var result struct {
		Code int
		Msg  string
		Data json.RawMessage
	}
	f.decode(rec.Body.Bytes(), &result)
	if rec.Code != status {
		f.t.Fatalf("%s %s: got %d want %d: %s", user, route, rec.Code, status, rec.Body)
	}
	if status >= 400 && (result.Code != status || result.Msg != http.StatusText(status)) {
		f.t.Fatalf("nonstandard error %s", rec.Body)
	}
	return result.Data
}
func (f *runtimeFixture) request(key string) map[string]any {
	return map[string]any{"conversationId": f.conversation.ID, "expectedVersion": f.conversation.Version, "image": "python:3.12", "ttl": 600, "requestKey": key}
}
func (f *runtimeFixture) start(key string) domain.RunContext {
	f.t.Helper()
	var r domain.RunContext
	f.decode(f.call("alice", "conversation/continue", f.request(key), 200), &r)
	return r
}
func (f *runtimeFixture) step(id string) domain.RunContext {
	f.t.Helper()
	ctx := context.Background()
	if _, e := f.s.DB.Exec(ctx, "UPDATE runtime_operations SET next_attempt_at=now()-interval '1 hour' WHERE id=$1", id); e != nil {
		f.t.Fatal(e)
	}
	if claimed, e := f.p.ProcessNextPreparation(ctx, "test-worker"); e != nil || !claimed {
		f.t.Fatalf("worker: %v %v", claimed, e)
	}
	r, e := f.s.Run(ctx, id)
	if e != nil {
		f.t.Fatal(e)
	}
	return r
}
func (f *runtimeFixture) tool(r domain.RunContext, status int) {
	f.t.Helper()
	f.call("alice", "sandbox/action", map[string]any{"id": r.SandboxID, "runContextId": r.ID, "action": "command", "command": "echo hello"}, status)
}
func TestContinueRunLifecycleAndPinnedActions(t *testing.T) {
	f := newRuntimeFixture(t)
	ctx := context.Background()
	r := f.start("request-one")
	if r.State != "Preparing" {
		t.Fatal(r)
	}
	again := f.start("request-two")
	if again.ID != r.ID {
		t.Fatal("duplicate continue created context")
	}
	changed := f.request("request-one")
	changed["ttl"] = 1200
	f.call("alice", "conversation/continue", changed, 409)
	f.call("bob", "conversation/continue", f.request("bob-request"), 404)
	f.call("bob", "run/status", map[string]any{"id": r.ID}, 404)
	f.call("alice", "conversation/workspace", map[string]any{"id": f.conversation.ID, "workspaceId": "", "expectedVersion": 1}, 409)
	f.call("alice", "workspace/action", map[string]any{"id": f.workspace.ID, "action": "archive"}, 409)
	var other domain.Conversation
	f.decode(f.call("alice", "conversation/register", map[string]any{"projectId": f.project, "integrationId": "test", "externalId": "thread-2", "title": "other"}, 200), &other)
	// A new binding to an occupied workspace is rejected as well.
	f.call("alice", "conversation/workspace", map[string]any{"id": other.ID, "workspaceId": f.workspace.ID, "expectedVersion": 0}, 409)
	r = f.step(r.ID)
	if r.State != "Ready" || r.SandboxID == "" || r.AttachmentGeneration != 1 {
		t.Fatalf("not ready: %+v", r)
	}
	f.tool(r, 200)
	f.call("alice", "sandbox/action", map[string]any{"id": r.SandboxID, "action": "command", "command": "echo bypass"}, 409)
	f.call("bob", "sandbox/action", map[string]any{"id": r.SandboxID, "runContextId": r.ID, "action": "command", "command": "echo bypass"}, 404)
	b, e := f.s.Get(ctx, r.SandboxID)
	if e != nil {
		t.Fatal(e)
	}
	release, e := f.s.GuardSandbox(ctx, b, r.ID, "alice", true)
	if e != nil {
		t.Fatal(e)
	}
	// Reconciliation cannot make this command's otherwise valid context unusable.
	if observed := f.step(r.ID); observed.State != "Ready" {
		t.Fatalf("health check suspended an active command: %+v", observed)
	}
	f.tool(r, 409)
	f.call("alice", "run/release", map[string]any{"id": r.ID}, 409)
	release()
	f.call("alice", "run/release", map[string]any{"id": r.ID}, 200)
	f.tool(r, 409)
	next := f.start("request-three")
	next = f.step(next.ID)
	if next.ID == r.ID || next.SandboxID != r.SandboxID || next.State != "Ready" {
		t.Fatal("did not reuse running sandbox")
	}
	f.backend.mu.Lock()
	creates := f.backend.creates
	f.backend.mu.Unlock()
	if creates != 1 {
		t.Fatal("duplicate upstream create")
	}
	f.call("alice", "sandbox/action", map[string]any{"id": next.SandboxID, "action": "delete"}, 200)
	next = f.step(next.ID)
	if next.State != "Invalidated" || next.ReleasedAt == nil {
		t.Fatalf("expired context not invalidated: %+v", next)
	}
	third := f.start("request-four")
	third = f.step(third.ID)
	if third.State != "Ready" || third.SandboxID == r.SandboxID || third.AttachmentGeneration != 2 {
		t.Fatalf("not restored to new runtime: %+v", third)
	}
	f.tool(r, 409)
	f.tool(next, 409)
	f.tool(third, 200)
	w, e := f.s.Workspace(ctx, f.workspace.ID)
	if e != nil || w.PVCUID != f.workspace.PVCUID {
		t.Fatal("persistent identity changed")
	}
	f.backend.mu.Lock()
	if len(f.backend.claims) != 2 || f.backend.claims[0] != f.backend.claims[1] {
		t.Error("recreated runtime uses different PVC")
	}
	f.backend.mu.Unlock()
	var auditRun, auditActor string
	if e = f.s.DB.QueryRow(ctx, "SELECT run_context_id,actor FROM audit_events WHERE action='command' AND outcome='success' ORDER BY id DESC LIMIT 1").Scan(&auditRun, &auditActor); e != nil || auditRun != third.ID || auditActor != "alice" {
		t.Fatal("missing pinned action audit", e)
	}
}
func TestContinueConcurrentRequestsAdmitOneContext(t *testing.T) {
	f := newRuntimeFixture(t)
	ctx := context.Background()
	version := f.conversation.Version
	var wg sync.WaitGroup
	ids := make(chan string, 16)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			r, e := f.s.BeginRun(ctx, domain.ContinueRequest{ConversationID: f.conversation.ID, ExpectedVersion: &version, Image: "python:3.12", TTL: 600, RequestKey: fmt.Sprintf("concurrent-%d", i)}, "alice", false)
			if e != nil {
				t.Error(e)
				return
			}
			ids <- r.ID
		}(i)
	}
	wg.Wait()
	close(ids)
	one := ""
	for id := range ids {
		if one != "" && one != id {
			t.Fatal("multiple active contexts")
		}
		one = id
	}
	if one == "" {
		t.Fatal("none admitted")
	}
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, e := f.p.ProcessNextPreparation(ctx, fmt.Sprintf("replica-%d", i))
			if e != nil {
				t.Error(e)
			}
		}(i)
	}
	wg.Wait()
	r, e := f.s.Run(ctx, one)
	if e != nil || r.State != "Ready" {
		t.Fatal(r, e)
	}
	f.backend.mu.Lock()
	defer f.backend.mu.Unlock()
	if f.backend.creates != 1 {
		t.Fatalf("%d remote creates", f.backend.creates)
	}
}
func TestContinueUnknownCreateNeverReplays(t *testing.T) {
	for _, mode := range []string{"lost", "empty"} {
		t.Run(mode, func(t *testing.T) {
			f := newRuntimeFixture(t)
			f.backend.mode = mode
			r := f.start("uncertain-request")
			r = f.step(r.ID)
			if r.State != "Waiting" || r.Reason != "create_unknown" {
				t.Fatal(r)
			}
			f.call("alice", "run/release", map[string]any{"id": r.ID}, 409)
			r = f.step(r.ID)
			if mode == "lost" && r.State != "Ready" {
				t.Fatal("lost response did not recover", r)
			}
			if mode == "empty" && (r.State != "Waiting" || r.ReleasedAt != nil) {
				t.Fatal("uncertain request released reservation")
			}
			f.backend.mu.Lock()
			defer f.backend.mu.Unlock()
			if f.backend.creates != 1 {
				t.Fatal("uncertain create replayed")
			}
		})
	}
}
func TestContinueLeaseRecoveryAndFencing(t *testing.T) {
	for _, sent := range []bool{false, true} {
		t.Run(fmt.Sprintf("sent=%v", sent), func(t *testing.T) {
			f := newRuntimeFixture(t)
			ctx := context.Background()
			r := f.start("recover-request")
			old, e := f.s.ClaimPreparation(ctx, "old-process")
			if e != nil {
				t.Fatal(e)
			}
			b, e := f.s.ReserveRunSandbox(ctx, old, false)
			if e != nil {
				t.Fatal(e)
			}
			if sent {
				ok, e := f.s.MarkCreateSent(ctx, old)
				if e != nil || !ok {
					t.Fatal(e)
				}
				f.backend.resources["lost-remote"] = map[string]any{"id": "lost-remote", "status": map[string]string{"state": "Running"}, "metadata": map[string]string{"operationId": b.ID}}
			}
			if _, e = f.s.DB.Exec(ctx, "UPDATE runtime_operations SET lease_until=now()-interval '1 second' WHERE id=$1", r.ID); e != nil {
				t.Fatal(e)
			}
			// Simulate a restarted process sharing only PostgreSQL and the remote system.
			f.p = &application.Platform{Store: f.s, Backend: f.p.Backend, Storage: f.volumes}
			r = f.step(r.ID)
			if r.State != "Ready" {
				t.Fatal(r)
			}
			if e = f.s.SavePreparedSandbox(ctx, old, "stale", "Running", nil); e != store.ErrLeaseLost {
				t.Fatalf("stale save accepted: %v", e)
			}
			if e = f.s.FinishPreparation(ctx, old, "Failed", "stale", "bad"); e != store.ErrLeaseLost {
				t.Fatalf("stale finish accepted: %v", e)
			}
			f.backend.mu.Lock()
			defer f.backend.mu.Unlock()
			want := 1
			if sent {
				want = 0
			}
			if f.backend.creates != want {
				t.Fatalf("replayed marked create: %d", f.backend.creates)
			}
		})
	}
}
func TestContinueBindingAndPermissionChanges(t *testing.T) {
	f := newRuntimeFixture(t)
	ctx := context.Background()
	q := f.request("bad-version")
	q["expectedVersion"] = 0
	f.call("alice", "conversation/continue", q, 409)
	f.call("alice", "conversation/workspace", map[string]any{"id": f.conversation.ID, "workspaceId": "", "expectedVersion": 1}, 200)
	q = f.request("no-workspace")
	q["expectedVersion"] = 2
	f.call("alice", "conversation/continue", q, 409)
	f.decode(f.call("alice", "conversation/workspace", map[string]any{"id": f.conversation.ID, "workspaceId": f.workspace.ID, "expectedVersion": 2}, 200), &f.conversation)
	r := f.start("before-revoke")
	if _, e := f.s.DB.Exec(ctx, "DELETE FROM project_members WHERE project_id=$1 AND user_id='alice'", f.project); e != nil {
		t.Fatal(e)
	}
	r = f.step(r.ID)
	if r.State != "Failed" || r.Reason != "permission_revoked" {
		t.Fatal(r)
	}
	if f.backend.creates != 0 {
		t.Fatal("revoked user reached remote create")
	}
}

func TestContinueSharedWorkspaceAndChangedAttachment(t *testing.T) {
	f := newRuntimeFixture(t)
	var other domain.Conversation
	f.decode(f.call("alice", "conversation/register", map[string]any{"projectId": f.project, "integrationId": "test", "externalId": "second-thread", "title": "second"}, 200), &other)
	f.decode(f.call("alice", "conversation/workspace", map[string]any{"id": other.ID, "workspaceId": f.workspace.ID, "expectedVersion": 0}, 200), &other)
	r := f.start("first-context")
	q := f.request("second-context")
	q["conversationId"] = other.ID
	q["expectedVersion"] = other.Version
	f.call("alice", "conversation/continue", q, 409)
	r = f.step(r.ID)
	if _, e := f.s.DB.Exec(context.Background(), "UPDATE workspaces SET attachment_generation=attachment_generation+1 WHERE id=$1", f.workspace.ID); e != nil {
		t.Fatal(e)
	}
	before := f.backend.dataCalls
	f.tool(r, 409)
	if f.backend.dataCalls != before {
		t.Fatal("stale attachment reached execd")
	}
}
func TestContinueWaitsForStorageAndOldConsumers(t *testing.T) {
	f := newRuntimeFixture(t)
	f.volumes.broken = true
	r := f.start("storage-request")
	r = f.step(r.ID)
	if r.State != "Waiting" || r.Reason != "storage_unavailable" || f.backend.creates != 0 {
		t.Fatal(r)
	}
	f.volumes.broken = false
	f.volumes.used = true
	r = f.step(r.ID)
	if r.State != "Waiting" || r.Reason != "external_consumer" || f.backend.creates != 0 {
		t.Fatal(r)
	}
	f.volumes.used = false
	r = f.step(r.ID)
	if r.State != "Ready" || f.backend.creates != 1 {
		t.Fatal(r)
	}
	f.volumes.used = true
	f.call("alice", "sandbox/action", map[string]any{"id": r.SandboxID, "action": "delete"}, 200)
	r = f.step(r.ID)
	if r.State != "Invalidated" {
		t.Fatal(r)
	}
	next := f.start("waiting-consumer")
	next = f.step(next.ID)
	if next.State != "Waiting" || next.Reason != "consumer_exiting" || f.backend.creates != 1 {
		t.Fatal(next)
	}
	f.volumes.used = false
	next = f.step(next.ID)
	if next.State != "Ready" || f.backend.creates != 2 {
		t.Fatal(next)
	}
}

func TestContinueListRetainsActiveContextBeyondHistoryLimit(t *testing.T) {
	f := newRuntimeFixture(t)
	active := f.start("old-active-context")
	ctx := context.Background()
	_, e := f.s.DB.Exec(ctx, `WITH history AS (
 INSERT INTO run_contexts(id,project_id,owner_id,actor,conversation_id,workspace_id,binding_version,image,ttl,state,released_at,create_by,update_by,create_at)
 SELECT 'history-'||i,project_id,owner_id,actor,conversation_id,workspace_id,binding_version,image,ttl,'Released',now(),actor,actor,now()+interval '1 second' FROM run_contexts CROSS JOIN generate_series(1,110) i WHERE id=$1 RETURNING id
 ) INSERT INTO runtime_operations(id,state,create_by,update_by) SELECT id,'Cancelled','alice','alice' FROM history`, active.ID)
	if e != nil {
		t.Fatal(e)
	}
	var runs []domain.RunContext
	f.decode(f.call("alice", "run/list", map[string]any{"projectId": f.project}, 200), &runs)
	found := false
	for _, r := range runs {
		if r.ID == active.ID {
			found = true
		}
	}
	if !found || len(runs) != 101 {
		t.Fatalf("active context disappeared among historical entries: found=%v count=%d", found, len(runs))
	}
}

func TestContinueSandboxListRetainsOldActiveInstance(t *testing.T) {
	f := newRuntimeFixture(t)
	r := f.start("old-active-sandbox")
	r = f.step(r.ID)
	ctx := context.Background()
	_, e := f.s.DB.Exec(ctx, `INSERT INTO sandboxes(id,project_id,owner_id,name,image,state,request_key,ttl,create_at,create_by,update_by)
 SELECT 'history-'||i,project_id,owner_id,'history',image,'Deleted','history-'||i,ttl,now()+interval '1 second',owner_id,owner_id FROM sandboxes CROSS JOIN generate_series(1,210) i WHERE id=$1`, r.SandboxID)
	if e != nil {
		t.Fatal(e)
	}
	var boxes []domain.Sandbox
	f.decode(f.call("alice", "sandbox/list", map[string]any{"projectId": f.project}, 200), &boxes)
	found := false
	for _, b := range boxes {
		if b.ID == r.SandboxID {
			found = true
		}
	}
	if !found || len(boxes) != 201 {
		t.Fatalf("active sandbox disappeared: found=%v count=%d", found, len(boxes))
	}
}
