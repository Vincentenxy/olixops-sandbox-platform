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
	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"net/http"
	"net/http/httptest"
	"net/url"
	"olixops-agent-platform/internal/application"
	"olixops-agent-platform/internal/domain"
	"olixops-agent-platform/internal/infrastructure/config"
	"olixops-agent-platform/internal/infrastructure/sandbox"
	"olixops-agent-platform/internal/infrastructure/store"
	"olixops-agent-platform/internal/interfaces/router"
	"olixops-agent-platform/pkg/logger"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func db(t *testing.T) *store.Store {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_DSN")
	if dsn == "" {
		t.Skip("set TEST_DATABASE_DSN to run PostgreSQL integration tests")
	}
	ctx := context.Background()
	pool, e := pgxpool.New(ctx, dsn)
	if e != nil {
		t.Fatal(e)
	}
	schema := "test_" + store.ID()
	if _, e = pool.Exec(ctx, "CREATE SCHEMA "+schema); e != nil {
		t.Fatal(e)
	}
	u, _ := url.Parse(dsn)
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	s, e := store.Open(ctx, u.String(), true)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.DB.Close(); _, _ = pool.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE"); pool.Close() })
	return s
}
func TestPlatformIsolationAndLifecycle(t *testing.T) {
	logger.Init("test")
	s := db(t)
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	pub, _ := x509.MarshalPKIXPublicKey(&key.PublicKey)
	cfg, e := config.Load("../../../configs")
	if e != nil {
		t.Fatal(e)
	}
	cfg.Server.Mode = "test"
	cfg.Auth.Local.Enabled = false
	cfg.Auth.JwtPublicKey = string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pub}))
	cfg.Auth.Admins = nil
	cfg.Sandbox.DefaultQuota = 3
	var creates atomic.Int32
	var dataRequests atomic.Int32
	var largeLogs atomic.Bool
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("OPEN-SANDBOX-API-KEY") != "backend-secret" {
			t.Errorf("missing upstream credential for %s", r.URL.Path)
		}
		switch {
		case r.Method == "POST" && r.URL.Path == "/osb/v1/sandboxes":
			creates.Add(1)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(202)
			fmt.Fprint(w, `{"id":"remote-1","status":{"state":"Running"},"createdAt":"2026-10-30T00:00:00Z"}`)
		case strings.Contains(r.URL.Path, "/endpoints/"):
			fmt.Fprint(w, `{"endpoint":"attacker.invalid/sandboxes/remote-1/proxy/44772"}`)
		case strings.HasSuffix(r.URL.Path, "/files/upload"):
			dataRequests.Add(1)
			if e := r.ParseMultipartForm(5 << 20); e != nil {
				t.Error(e)
			}
			w.WriteHeader(200)
			fmt.Fprint(w, `{}`)
		case strings.HasSuffix(r.URL.Path, "/files/download"):
			dataRequests.Add(1)
			fmt.Fprint(w, "hello 文件")
		case strings.HasSuffix(r.URL.Path, "/directories/list"):
			dataRequests.Add(1)
			fmt.Fprint(w, `[{"path":"/workspace/app.py","name":"app.py","isDir":false}]`)
		case strings.HasSuffix(r.URL.Path, "/logs"):
			if r.URL.Query().Get("cursor") != "5" {
				t.Errorf("cursor not forwarded")
			}
			w.Header().Set("EXECD-COMMANDS-TAIL-CURSOR", "12")
			if largeLogs.Load() {
				fmt.Fprint(w, strings.Repeat("x", (1<<20)+1))
				return
			}
			fmt.Fprint(w, "new log")
		case strings.HasSuffix(r.URL.Path, "/command"):
			dataRequests.Add(1)
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "event: stdout\ndata: {\"text\":\"hello\"}\n\nevent: execution_complete\ndata: {}\n\n")
		case strings.HasSuffix(r.URL.Path, "/health"):
			dataRequests.Add(1)
			fmt.Fprint(w, `{"status":"ok"}`)
		case r.Method == "DELETE":
			w.WriteHeader(204)
		default:
			fmt.Fprint(w, `{"id":"remote-1","status":{"state":"Running"}}`)
		}
	}))
	defer upstream.Close()
	p := &application.Platform{Store: s, Backend: sandbox.New(upstream.URL+"/osb", "backend-secret")}
	r, e := router.New(cfg, p)
	if e != nil {
		t.Fatal(e)
	}
	sign := func(user string, claims jwt.MapClaims) string {
		if claims == nil {
			claims = jwt.MapClaims{"staffuserid": user, "exp": time.Now().Add(time.Hour).Unix(), "iss": "olixops-local", "aud": "olixops-agent-platform"}
		}
		raw, e := jwt.NewWithClaims(jwt.SigningMethodRS256, claims).SignedString(key)
		if e != nil {
			t.Fatal(e)
		}
		return raw
	}
	tokens := map[string]string{"alice": sign("alice", nil), "bob": sign("bob", nil)}
	call := func(user, route string, body any, want int) json.RawMessage {
		t.Helper()
		var b []byte
		if body != nil {
			b, _ = json.Marshal(body)
		}
		method := "GET"
		if body != nil {
			method = "POST"
		}
		req := httptest.NewRequest(method, "/api/v1/"+route, bytes.NewReader(b))
		req.Header.Set("Authorization", "Bearer "+tokens[user])
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		var v struct {
			Code int             `json:"code"`
			Msg  string          `json:"msg"`
			Data json.RawMessage `json:"data"`
		}
		if e := json.Unmarshal(w.Body.Bytes(), &v); e != nil {
			t.Fatalf("nonstandard response %s", w.Body)
		}
		if w.Code != want {
			t.Fatalf("%s: got %d want %d body %s", route, w.Code, want, w.Body)
		}
		if want >= 400 && (v.Code != want || v.Msg != http.StatusText(want)) {
			t.Fatal("invalid error envelope")
		}
		return v.Data
	}
	call("", "user/profile", nil, 401)
	tokens["missingId"] = sign("", nil)
	call("missingId", "user/profile", nil, 401)
	tokens["noExpiry"] = sign("alice", jwt.MapClaims{"staffuserid": "alice", "iss": "olixops-local", "aud": "olixops-agent-platform"})
	call("noExpiry", "user/profile", nil, 401)
	raw, _ := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{"staffuserid": "alice", "exp": time.Now().Add(time.Hour).Unix()}).SignedString([]byte("bad"))
	tokens["hs"] = raw
	call("hs", "user/profile", nil, 401)
	var project struct{ ID string }
	_ = json.Unmarshal(call("alice", "project/create", map[string]any{"name": "AI Project"}, 200), &project)
	create := map[string]any{"projectId": project.ID, "name": "workspace", "image": "python:3.12", "ttl": 3600, "requestKey": "same-request-123"}
	call("bob", "sandbox/create", create, 404)
	call("alice", "project/member/set", map[string]any{"projectId": project.ID, "userId": "bob", "role": "member"}, 200)
	var created struct {
		Sandbox domain.Sandbox `json:"sandbox"`
	}
	_ = json.Unmarshal(call("alice", "sandbox/create", create, 200), &created)
	if created.Sandbox.ID == "" {
		t.Fatal("no sandbox")
	}
	call("alice", "sandbox/create", create, 200)
	create["name"] = "changed"
	call("alice", "sandbox/create", create, 409)
	create["name"] = "workspace"
	if creates.Load() != 1 {
		t.Fatalf("duplicate upstream create: %d", creates.Load())
	}
	for _, action := range []string{"sync", "delete", "renew", "command", "logs", "files", "read", "write", "remove", "preview"} {
		call("bob", "sandbox/action", map[string]any{"id": created.Sandbox.ID, "action": action}, 404)
	}
	if dataRequests.Load() != 0 {
		t.Fatal("unauthorized request reached upstream")
	}
	var list []domain.Sandbox
	_ = json.Unmarshal(call("bob", "sandbox/list", map[string]any{"projectId": project.ID}, 200), &list)
	if len(list) != 0 {
		t.Fatal("list leaked another user's sandbox")
	}
	call("bob", "project/member/set", map[string]any{"projectId": project.ID, "userId": "bob", "role": "maintainer"}, 403)
	call("alice", "sandbox/action", map[string]any{"id": created.Sandbox.ID, "action": "write", "path": "/workspace/app.py", "content": "aGVsbG8="}, 200)
	file := call("alice", "sandbox/action", map[string]any{"id": created.Sandbox.ID, "action": "read", "path": "/workspace/app.py"}, 200)
	if !bytes.Contains(file, []byte("aGVsbG8g5paH5Lu2")) {
		t.Fatal("file content was not round-tripped")
	}
	files := call("alice", "sandbox/action", map[string]any{"id": created.Sandbox.ID, "action": "files", "path": "/workspace"}, 200)
	if !bytes.Contains(files, []byte(`"modifiedAt"`)) || bytes.Contains(files, []byte(`"modified_at"`)) {
		t.Fatalf("nonstandard file response: %s", files)
	}
	events := call("alice", "sandbox/action", map[string]any{"id": created.Sandbox.ID, "action": "command", "command": "echo hello"}, 200)
	if !bytes.Contains(events, []byte(`"event":"stdout"`)) || bytes.Contains(events, []byte(`"Data"`)) {
		t.Fatalf("nonstandard event response: %s", events)
	}
	logs := call("alice", "sandbox/action", map[string]any{"id": created.Sandbox.ID, "action": "logs", "commandId": "background", "cursor": 5}, 200)
	if !bytes.Contains(logs, []byte(`"output":"new log"`)) || !bytes.Contains(logs, []byte(`"cursor":12`)) {
		t.Fatalf("nonstandard incremental logs: %s", logs)
	}
	call("alice", "sandbox/action", map[string]any{"id": created.Sandbox.ID, "action": "logs", "commandId": "background", "cursor": -1}, 400)
	largeLogs.Store(true)
	call("alice", "sandbox/action", map[string]any{"id": created.Sandbox.ID, "action": "logs", "commandId": "background", "cursor": 5}, 413)
	call("alice", "sandbox/action", map[string]any{"id": created.Sandbox.ID, "action": "preview", "port": 8000, "path": "/health"}, 200)
	call("alice", "sandbox/action", map[string]any{"id": created.Sandbox.ID, "action": "preview", "port": 44772, "path": "/health"}, 400)
	call("alice", "sandbox/action", map[string]any{"id": created.Sandbox.ID, "action": "preview", "port": 8000, "path": "http://evil.invalid/"}, 400)
	for _, attack := range []string{"/../../v1/sandboxes", "/%2e%2e/%2e%2e/v1/sandboxes", "/%252e%252e/v1/sandboxes", "/\\../v1/sandboxes"} {
		call("alice", "sandbox/action", map[string]any{"id": created.Sandbox.ID, "action": "preview", "port": 8000, "path": attack}, 400)
	}
	call("alice", "sandbox/action", map[string]any{"id": created.Sandbox.ID, "action": "delete"}, 200)
	b, e := s.Get(context.Background(), created.Sandbox.ID)
	if e != nil || b.State != "Deleted" {
		t.Fatalf("delete not persisted: %v %s", e, b.State)
	}
	// A late sync cannot resurrect a deleted resource.
	_ = s.Update(context.Background(), b.ID, "remote-1", "Running", nil)
	b, _ = s.Get(context.Background(), b.ID)
	if b.State != "Deleted" {
		t.Fatal("late status update resurrected sandbox")
	}
	audit := call("alice", "audit/list", map[string]any{"projectId": project.ID}, 200)
	if !bytes.Contains(audit, []byte("sandbox.create")) {
		t.Fatal("missing audit")
	}
	call("alice", "missing-route", nil, 404)
}
func TestQuotaAdmissionIsAtomic(t *testing.T) {
	s := db(t)
	ctx := context.Background()
	p, e := s.CreateProject(ctx, "owner", "quota test", 3)
	if e != nil {
		t.Fatal(e)
	}
	var count atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, fresh, e := s.Reserve(ctx, domain.Sandbox{ProjectID: p, OwnerID: "owner", Name: "x", Image: "python:3.12", TTL: 60, RequestKey: fmt.Sprintf("request-%d", i)}, "owner")
			if e == nil && fresh {
				count.Add(1)
			} else if e != store.ErrQuota {
				t.Errorf("reserve: %v", e)
			}
		}(i)
	}
	wg.Wait()
	if count.Load() != 3 {
		t.Fatalf("admitted %d expected 3", count.Load())
	}
}

func TestUncertainCreateDoesNotReplay(t *testing.T) {
	s := db(t)
	ctx := context.Background()
	project, e := s.CreateProject(ctx, "owner", "uncertain", 1)
	if e != nil {
		t.Fatal(e)
	}
	var creates atomic.Int32
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			creates.Add(1)
			w.WriteHeader(503)
			fmt.Fprint(w, `{"code":"unavailable","message":"try later"}`)
			return
		}
		fmt.Fprint(w, `{"items":[{"id":"created-but-response-lost","status":{"state":"Running"}}],"pagination":{"totalItems":1}}`)
	}))
	defer up.Close()
	p := &application.Platform{Store: s, Backend: sandbox.New(up.URL, "")}
	input := domain.Sandbox{ProjectID: project, OwnerID: "owner", Name: "x", Image: "python:3.12", TTL: 3600, RequestKey: "uncertain-key"}
	b, e := p.Create(ctx, input, "owner")
	if e == nil || b.State != "Unknown" {
		t.Fatalf("expected Unknown: %s %v", b.State, e)
	}
	again, e := p.Create(ctx, input, "owner")
	if e != nil || again.ID != b.ID || creates.Load() != 1 {
		t.Fatal("uncertain create was replayed")
	}
	input.RequestKey = "new-key"
	if _, e := p.Create(ctx, input, "owner"); e != store.ErrQuota {
		t.Fatalf("Unknown did not reserve quota: %v", e)
	}
	_, _ = s.DB.Exec(ctx, "UPDATE sandboxes SET create_at=now()-interval '4 minutes' WHERE id=$1", b.ID)
	b, _ = s.Get(ctx, b.ID)
	if e = p.SyncOne(ctx, b); e != nil {
		t.Fatal(e)
	}
	b, _ = s.Get(ctx, b.ID)
	if b.BackendID != "created-but-response-lost" || b.State != "Running" {
		t.Fatal("resource not recovered")
	}
}
