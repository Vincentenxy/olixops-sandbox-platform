package router_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"olixops-sandbox-platform/internal/application"
	"olixops-sandbox-platform/internal/domain"
	"olixops-sandbox-platform/internal/infrastructure/sandbox"
	"olixops-sandbox-platform/internal/infrastructure/storage"
)

type observedVolume struct {
	calls         atomic.Int32
	missing       atomic.Bool
	concurrent    atomic.Int32
	maxConcurrent atomic.Int32
}

func (f *observedVolume) Ensure(ctx context.Context, w domain.Workspace) (storage.Observation, error) {
	active := f.concurrent.Add(1)
	defer f.concurrent.Add(-1)
	for old := f.maxConcurrent.Load(); active > old; old = f.maxConcurrent.Load() {
		if f.maxConcurrent.CompareAndSwap(old, active) {
			break
		}
	}
	if f.missing.Load() {
		if w.PVCUID == "" {
			f.calls.Add(1)
			return storage.Observation{UID: "replacement", Phase: "Pending"}, nil
		}
		return storage.Observation{}, errors.New("original PVC missing")
	}
	f.calls.Add(1)
	return storage.Observation{UID: "original", PV: "original-pv", Phase: "Bound"}, nil
}
func (*observedVolume) InUse(context.Context, domain.Workspace) (bool, error) { return false, nil }

func TestWorkspaceStaleSyncCannotRecreatePersistentData(t *testing.T) {
	s := db(t)
	ctx := context.Background()
	project, err := s.CreateProject(ctx, "owner", "stale observation", 3)
	if err != nil {
		t.Fatal(err)
	}
	original, err := s.ReserveWorkspace(ctx, domain.Workspace{ProjectID: project, OwnerID: "owner", Name: "data", RequestKey: "persistent-key", RequestedBytes: 10, StorageClass: "test", ClusterID: "test", KubeNamespace: "sandbox"}, 3, 30)
	if err != nil {
		t.Fatal(err)
	}
	volumes := &observedVolume{}
	p := &application.Platform{Store: s, Storage: volumes}
	if err = p.SyncWorkspace(ctx, original); err != nil {
		t.Fatal(err)
	}
	volumes.missing.Store(true)
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := p.SyncWorkspace(ctx, original); err == nil {
				t.Error("missing original accepted")
			}
		}()
	}
	wg.Wait()
	current, err := s.Workspace(ctx, original.ID)
	if err != nil {
		t.Fatal(err)
	}
	if volumes.calls.Load() != 1 || volumes.maxConcurrent.Load() != 1 || current.PVCUID != "original" || current.State != "Error" {
		t.Fatalf("stale sync recreated or lost data identity: creates=%d concurrent=%d row=%+v", volumes.calls.Load(), volumes.maxConcurrent.Load(), current)
	}
}

func TestWorkspaceArchiveLocksProjectBeforeWorkspace(t *testing.T) {
	s := db(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	project, err := s.CreateProject(ctx, "owner", "lock order", 3)
	if err != nil {
		t.Fatal(err)
	}
	w, err := s.ReserveWorkspace(ctx, domain.Workspace{ProjectID: project, OwnerID: "owner", Name: "data", RequestKey: "lock-order-key", RequestedBytes: 10, StorageClass: "test"}, 3, 30)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	var pid int
	var id string
	if err = tx.QueryRow(ctx, "SELECT pg_backend_pid(),id FROM projects WHERE id=$1 FOR UPDATE", project).Scan(&pid, &id); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- s.ArchiveWorkspace(ctx, w.ID, "owner", true) }()
	// Wait until archive actually blocks on this transaction, not a timing-based sleep.
	for {
		var blocked bool
		if err = s.DB.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE $1=ANY(pg_blocking_pids(pid)))", pid).Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("archive never waited on project")
		case <-time.After(5 * time.Millisecond):
		}
	}
	if err = tx.QueryRow(ctx, "SELECT id FROM workspaces WHERE id=$1 FOR UPDATE NOWAIT", w.ID).Scan(&id); err != nil {
		t.Fatalf("reverse workspace/project lock order: %v", err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err = <-done; err != nil {
		t.Fatal(err)
	}
}

func TestFailedSandboxRemainsTerminalAndDoesNotConsumeQuota(t *testing.T) {
	s := db(t)
	ctx := context.Background()
	project, err := s.CreateProject(ctx, "owner", "failed terminal", 1)
	if err != nil {
		t.Fatal(err)
	}
	b, _, err := s.Reserve(ctx, domain.Sandbox{ProjectID: project, OwnerID: "owner", Name: "invalid", Image: "python:3.12", TTL: 600, RequestKey: "failed-request"}, "owner")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Update(ctx, b.ID, "", "Failed", nil); err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB.Exec(ctx, "UPDATE sandboxes SET create_at=now()-interval '4 minutes' WHERE id=$1", b.ID); err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int32
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1); fmt.Fprint(w, `{"items":[]}`) }))
	defer up.Close()
	p := &application.Platform{Store: s, Backend: sandbox.New(up.URL, "")}
	b, err = s.Get(ctx, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = p.SyncOne(ctx, b); err != nil {
		t.Fatal(err)
	}
	if err = s.Update(ctx, b.ID, "", "Unknown", nil); err != nil {
		t.Fatal(err)
	} // stale in-flight update
	b, err = s.Get(ctx, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	if b.State != "Failed" || requests.Load() != 0 {
		t.Fatalf("terminal resurrected: %s, upstream=%d", b.State, requests.Load())
	}
	if _, fresh, err := s.Reserve(ctx, domain.Sandbox{ProjectID: project, OwnerID: "owner", Name: "next", Image: "python:3.12", TTL: 600, RequestKey: "next-request"}, "owner"); err != nil || !fresh {
		t.Fatalf("failed record holds quota: %v", err)
	}
}

type unavailableStorage struct{}

func (unavailableStorage) Ensure(ctx context.Context, _ domain.Workspace) (storage.Observation, error) {
	<-ctx.Done()
	return storage.Observation{}, ctx.Err()
}
func (unavailableStorage) InUse(context.Context, domain.Workspace) (bool, error) {
	return false, errors.New("storage API unavailable")
}

func TestWorkspaceStorageOutageDoesNotBlockSandboxReconcile(t *testing.T) {
	s := db(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	project, err := s.CreateProject(ctx, "owner", "independent workers", 3)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ReserveWorkspace(ctx, domain.Workspace{ProjectID: project, OwnerID: "owner", Name: "data", RequestKey: "outage-workspace", RequestedBytes: 10, StorageClass: "test"}, 3, 30); err != nil {
		t.Fatal(err)
	}
	b, _, err := s.Reserve(ctx, domain.Sandbox{ProjectID: project, OwnerID: "owner", Name: "runtime", Image: "python:3.12", TTL: 600, RequestKey: "outage-runtime"}, "owner")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Update(ctx, b.ID, "remote", "Running", nil); err != nil {
		t.Fatal(err)
	}
	seen := make(chan struct{}, 1)
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case seen <- struct{}{}:
		default:
		}
		fmt.Fprint(w, `{"id":"remote","status":{"state":"Running"}}`)
	}))
	defer up.Close()
	p := &application.Platform{Store: s, Storage: unavailableStorage{}, Backend: sandbox.New(up.URL, "")}
	done := make(chan struct{})
	go func() { defer close(done); p.Reconcile(ctx) }()
	defer func() {
		cancel()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("worker did not stop after cancellation")
		}
	}()
	select {
	case <-seen:
	case <-time.After(18 * time.Second):
		t.Fatal("storage outage blocked sandbox lifecycle reconciliation")
	}
}
