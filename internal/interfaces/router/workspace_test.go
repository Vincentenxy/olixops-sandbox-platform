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
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"olixops-sandbox-platform/internal/application"
	"olixops-sandbox-platform/internal/domain"
	"olixops-sandbox-platform/internal/infrastructure/config"
	"olixops-sandbox-platform/internal/infrastructure/sandbox"
	"olixops-sandbox-platform/internal/infrastructure/storage"
	"olixops-sandbox-platform/internal/infrastructure/store"
	"olixops-sandbox-platform/internal/interfaces/router"
	"olixops-sandbox-platform/pkg/logger"
)

type fakeVolumes struct {
	mu     sync.Mutex
	claims map[string]string
	used   bool
	broken bool
}

func (f *fakeVolumes) Ensure(_ context.Context, w domain.Workspace) (storage.Observation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.broken {
		return storage.Observation{}, fmt.Errorf("offline")
	}
	if f.claims[w.ID] == "" {
		f.claims[w.ID] = "uid-" + w.ID
	}
	return storage.Observation{UID: f.claims[w.ID], Phase: "Pending"}, nil
}
func (f *fakeVolumes) InUse(context.Context, domain.Workspace) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.used, nil
}
func TestWorkspaceNamespaceConversationAndRuntime(t *testing.T) {
	logger.Init("test")
	s := db(t)
	ctx := context.Background()
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	pub, _ := x509.MarshalPKIXPublicKey(&key.PublicKey)
	cfg, e := config.Load("../../../configs")
	if e != nil {
		t.Fatal(e)
	}
	cfg.Auth.Local.Enabled = false
	cfg.Server.Mode = "test"
	cfg.Auth.JwtPublicKey = string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pub}))
	cfg.Auth.Admins = nil
	volumes := &fakeVolumes{claims: map[string]string{}}
	var mu sync.Mutex
	creates := 0
	deleted := false
	uncertain := false
	var claimed string
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if r.Method == "POST" {
			creates++
			var q struct {
				Volumes []struct {
					PVC struct {
						ClaimName string `json:"claimName"`
					} `json:"pvc"`
					MountPath string `json:"mountPath"`
				} `json:"volumes"`
			}
			_ = json.NewDecoder(r.Body).Decode(&q)
			if len(q.Volumes) != 1 || q.Volumes[0].MountPath != "/workspace" {
				t.Errorf("invalid volume request")
			}
			claimed = q.Volumes[0].PVC.ClaimName
			if uncertain {
				w.WriteHeader(503)
				fmt.Fprint(w, `{"code":"busy","message":"unknown"}`)
				return
			}
			w.WriteHeader(202)
			fmt.Fprintf(w, `{"id":"remote-%d","status":{"state":"Running"}}`, creates)
			return
		}
		if r.Method == "DELETE" {
			deleted = true
			w.WriteHeader(204)
			return
		}
		if deleted {
			w.WriteHeader(404)
			fmt.Fprint(w, `{"code":"notfound","message":"gone"}`)
			return
		}
		fmt.Fprint(w, `{"id":"remote-1","status":{"state":"Running"}}`)
	}))
	defer up.Close()
	p := &application.Platform{Store: s, Backend: sandbox.New(up.URL, ""), Storage: volumes}
	r, e := router.New(cfg, p)
	if e != nil {
		t.Fatal(e)
	}
	call := func(user, route string, body any, status int) json.RawMessage {
		t.Helper()
		raw, _ := json.Marshal(body)
		method := "POST"
		if body == nil {
			method = "GET"
		}
		req := httptest.NewRequest(method, "/api/v1/"+route, bytes.NewReader(raw))
		token, _ := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{"staffuserid": user, "iss": "olixops-local", "aud": "olixops-agent-platform", "exp": time.Now().Add(time.Hour).Unix()}).SignedString(key)
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		var v struct {
			Code int
			Msg  string
			Data json.RawMessage
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &v)
		if rec.Code != status {
			t.Fatalf("%s got %d want %d: %s", route, rec.Code, status, rec.Body)
		}
		if status >= 400 && (v.Code != status || v.Msg != http.StatusText(status)) {
			t.Fatal("nonstandard error")
		}
		return v.Data
	}
	decode := func(b []byte, v any) {
		t.Helper()
		if e := json.Unmarshal(b, v); e != nil {
			t.Fatal(e)
		}
	}
	var ns, nsAgain, bobNS domain.Namespace
	decode(call("alice", "namespace/current", nil, 200), &ns)
	decode(call("alice", "namespace/current", nil, 200), &nsAgain)
	decode(call("bob", "namespace/current", nil, 200), &bobNS)
	if ns.ID != nsAgain.ID || ns.ID == bobNS.ID {
		t.Fatal("namespace identity not stable/user isolated")
	}
	var project struct{ ID string }
	decode(call("alice", "project/create", map[string]any{"name": "persistent"}, 200), &project)
	call("alice", "project/member/set", map[string]any{"projectId": project.ID, "userId": "bob", "role": "member"}, 200)
	create := map[string]any{"projectId": project.ID, "name": "app", "requestKey": "workspace-key-1"}
	var ws, again domain.Workspace
	decode(call("alice", "workspace/create", create, 200), &ws)
	decode(call("alice", "workspace/create", create, 200), &again)
	if ws.ID != again.ID || ws.NamespaceID != ns.ID || ws.State != "Available" || ws.PVCUID == "" {
		t.Fatal("workspace admission/pending/idempotency")
	}
	create["name"] = "changed"
	call("alice", "workspace/create", create, 409)
	create["name"] = "app"
	call("bob", "workspace/action", map[string]any{"id": ws.ID, "action": "sync"}, 404)
	var bobList []domain.Workspace
	decode(call("bob", "workspace/list", map[string]any{"projectId": project.ID}, 200), &bobList)
	if len(bobList) != 0 {
		t.Fatal("cross owner workspace leak")
	}
	var ownBob domain.Workspace
	decode(call("bob", "workspace/create", create, 200), &ownBob)
	conv := map[string]any{"projectId": project.ID, "integrationId": "local-ai", "externalId": "thread-1", "title": "order app"}
	var c, cAgain, bobC domain.Conversation
	decode(call("alice", "conversation/register", conv, 200), &c)
	decode(call("alice", "conversation/register", conv, 200), &cAgain)
	decode(call("bob", "conversation/register", conv, 200), &bobC)
	if c.ID != cAgain.ID || c.ID == bobC.ID {
		t.Fatal("external conversation identity collision")
	}
	bind := map[string]any{"id": c.ID, "workspaceId": ws.ID, "expectedVersion": 0}
	decode(call("alice", "conversation/workspace", bind, 200), &c)
	call("alice", "conversation/workspace", bind, 409)
	call("bob", "conversation/resolve", map[string]any{"id": c.ID}, 404)
	// Even project owner cannot bind someone else's data to their own conversation.
	call("alice", "conversation/workspace", map[string]any{"id": c.ID, "workspaceId": ownBob.ID, "expectedVersion": 1}, 404)
	resolved := call("alice", "conversation/resolve", map[string]any{"id": c.ID}, 200)
	if !bytes.Contains(resolved, []byte(ws.PVCName)) {
		t.Fatal("conversation did not resolve persistent volume")
	}
	start := map[string]any{"projectId": project.ID, "workspaceId": ws.ID, "name": "runtime", "image": "python:3.12", "ttl": 600, "requestKey": "runtime-request-1"}
	var runtime struct{ Sandbox domain.Sandbox }
	decode(call("alice", "sandbox/create", start, 200), &runtime)
	if runtime.Sandbox.WorkspaceID != ws.ID || claimed != ws.PVCName {
		t.Fatal("sandbox did not mount expected volume")
	}
	call("alice", "sandbox/create", start, 200)
	if creates != 1 {
		t.Fatal("runtime replay")
	}
	start["requestKey"] = "runtime-request-2"
	call("alice", "sandbox/create", start, 409)
	call("alice", "workspace/action", map[string]any{"id": ws.ID, "action": "archive"}, 409)
	call("alice", "conversation/workspace", map[string]any{"id": c.ID, "workspaceId": "", "expectedVersion": 1}, 409)
	volumes.mu.Lock()
	volumes.used = true
	volumes.mu.Unlock()
	call("alice", "sandbox/action", map[string]any{"id": runtime.Sandbox.ID, "action": "delete"}, 200)
	observed, _ := s.Workspace(ctx, ws.ID)
	if observed.ActiveSandboxID == "" {
		t.Fatal("released while Pod/CR consumer still exists")
	}
	volumes.mu.Lock()
	volumes.used = false
	volumes.mu.Unlock()
	call("alice", "workspace/action", map[string]any{"id": ws.ID, "action": "sync"}, 200)
	observed, _ = s.Workspace(ctx, ws.ID)
	if observed.ActiveSandboxID != "" || observed.PVCUID != ws.PVCUID {
		t.Fatal("original PVC not retained/released")
	}
	call("alice", "workspace/action", map[string]any{"id": ws.ID, "action": "archive"}, 200)
	call("alice", "sandbox/create", start, 409)
	call("alice", "workspace/action", map[string]any{"id": ws.ID, "action": "unarchive"}, 200)
	call("alice", "conversation/workspace", map[string]any{"id": c.ID, "workspaceId": "", "expectedVersion": 1}, 200)
	resolved = call("alice", "conversation/resolve", map[string]any{"id": c.ID}, 200)
	if !bytes.Contains(resolved, []byte(`"workspace":null`)) {
		t.Fatal("unbind did not resolve null")
	}
	observed, _ = s.Workspace(ctx, ws.ID)
	if observed.PVCUID != ws.PVCUID {
		t.Fatal("unbind changed data")
	}
	mu.Lock()
	uncertain = true
	deleted = false
	mu.Unlock()
	call("alice", "sandbox/create", start, 200)
	call("alice", "sandbox/create", start, 200)
	if creates != 2 {
		t.Fatal("uncertain create replayed")
	}
	observed, _ = s.Workspace(ctx, ws.ID)
	if observed.ActiveSandboxID == "" {
		t.Fatal("uncertain create lost lock")
	}

	// Delegated startup keeps the member's data ownership, but records the real operator.
	start["workspaceId"] = ownBob.ID
	start["requestKey"] = "delegated-runtime"
	decode(call("alice", "sandbox/create", start, 200), &runtime)
	if runtime.Sandbox.OwnerID != "bob" {
		t.Fatal("delegated startup changed data owner")
	}
	var auditActor, creator string
	if e := s.DB.QueryRow(ctx, "SELECT a.actor,b.create_by FROM audit_events a JOIN sandboxes b ON b.id=a.sandbox_id WHERE b.id=$1 AND a.action='sandbox.create'", runtime.Sandbox.ID).Scan(&auditActor, &creator); e != nil {
		t.Fatal(e)
	}
	if auditActor != "alice" || creator != "alice" {
		t.Fatalf("delegated actor recorded as resource owner: actor=%s creator=%s", auditActor, creator)
	}
}
func TestWorkspaceQuotaAndRuntimeAdmissionConcurrent(t *testing.T) {
	s := db(t)
	ctx := context.Background()
	project, e := s.CreateProject(ctx, "owner", "concurrent", 20)
	if e != nil {
		t.Fatal(e)
	}
	var mu sync.Mutex
	admitted := 0
	var w domain.Workspace
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			v, e := s.ReserveWorkspace(ctx, domain.Workspace{ProjectID: project, OwnerID: "owner", Name: "x", RequestKey: fmt.Sprintf("key-%d", i), RequestedBytes: 10, StorageClass: "test", ClusterID: "test", KubeNamespace: "sandbox"}, 3, 30)
			if e == nil {
				mu.Lock()
				admitted++
				w = v
				mu.Unlock()
			} else if e != store.ErrQuota {
				t.Errorf("reserve %v", e)
			}
		}(i)
	}
	wg.Wait()
	if admitted != 3 {
		t.Fatalf("quota: %d", admitted)
	}
	if e = s.ObserveWorkspace(ctx, w.ID, "uid", "pv", "Bound", "Available", ""); e != nil {
		t.Fatal(e)
	}
	admitted = 0
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, fresh, e := s.Reserve(ctx, domain.Sandbox{ProjectID: project, OwnerID: "owner", WorkspaceID: w.ID, Name: "x", Image: "python:3.12", TTL: 600, RequestKey: fmt.Sprintf("runtime-%d", i)}, "owner")
			if e == nil && fresh {
				mu.Lock()
				admitted++
				mu.Unlock()
			} else if e != store.ErrConflict {
				t.Errorf("runtime %v", e)
			}
		}(i)
	}
	wg.Wait()
	if admitted != 1 {
		t.Fatalf("exclusive mount: %d", admitted)
	}
}
