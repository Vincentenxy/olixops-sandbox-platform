package application

import (
	"context"
	"errors"
	osb "github.com/alibaba/OpenSandbox/sdks/sandbox/go"
	"go.uber.org/zap"
	"olixops-sandbox-platform/internal/domain"
	"olixops-sandbox-platform/internal/infrastructure/sandbox"
	"olixops-sandbox-platform/internal/infrastructure/storage"
	"olixops-sandbox-platform/internal/infrastructure/store"
	"olixops-sandbox-platform/pkg/logger"
	"sync"
	"time"
)

type Platform struct {
	Admins  []string
	Store   *store.Store
	Backend *sandbox.Client
	Storage storage.Service
}

func (p *Platform) Create(ctx context.Context, b domain.Sandbox, actor string) (domain.Sandbox, error) {
	if b.WorkspaceID != "" {
		if p.Storage == nil {
			return b, store.ErrConflict
		}
		w, e := p.Store.Workspace(ctx, b.WorkspaceID)
		if e != nil {
			return b, e
		}
		if e = p.SyncWorkspace(ctx, w); e != nil {
			return b, e
		}
		if w.ActiveSandboxID == "" {
			used, err := p.Storage.InUse(ctx, w)
			if err != nil {
				return b, err
			}
			if used {
				return b, store.ErrConflict
			}
		}
	}
	b, fresh, e := p.Store.Reserve(ctx, b, actor)
	if e != nil || !fresh {
		return b, e
	}
	state, backend, expires, e := p.createRemote(ctx, b)
	saveCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if saveErr := p.Store.Update(saveCtx, b.ID, backend, state, expires); saveErr != nil {
		logger.Error(saveCtx, "persist create outcome failed", zap.String("operationId", b.ID), zap.Error(saveErr))
		return b, saveErr
	}
	b, readErr := p.Store.Get(saveCtx, b.ID)
	if readErr != nil {
		return b, readErr
	}
	return b, e
}
func (p *Platform) createRemote(ctx context.Context, b domain.Sandbox) (string, string, *time.Time, error) {
	var volumes []osb.Volume
	if b.WorkspaceID != "" {
		w, err := p.Store.Workspace(ctx, b.WorkspaceID)
		if err != nil {
			return "Unknown", "", nil, err
		}
		volumes = []osb.Volume{{Name: "workspace", PVC: &osb.PVC{ClaimName: w.PVCName}, MountPath: "/workspace"}}
	}
	info, e := p.Backend.Lifecycle.CreateSandbox(ctx, osb.CreateSandboxRequest{Volumes: volumes, Image: &osb.ImageSpec{URI: b.Image}, Entrypoint: []string{"/bin/sh", "-c", "sleep infinity"}, Timeout: &b.TTL, ResourceLimits: osb.ResourceLimits{"cpu": "1", "memory": "1Gi"}, Metadata: map[string]string{"platform": sandboxPlatformIdentity, "operationId": b.ID}})
	// Timeouts can leave real resources behind. Unknown continues to occupy quota until reconciliation.
	state := "Unknown"
	backend := ""
	var expires *time.Time
	if e == nil {
		state = remoteState(info.Status.State)
		backend = info.ID
		expires = info.ExpiresAt
	} else {
		var api *osb.APIError
		if errors.As(e, &api) && api.StatusCode >= 400 && api.StatusCode < 500 && api.StatusCode != 408 && api.StatusCode != 429 {
			state = "Failed"
		}
	}
	return state, backend, expires, e
}
func (p *Platform) SyncOne(ctx context.Context, b domain.Sandbox) error {
	if domain.Terminal(b.State) {
		return nil
	}
	if b.BackendID == "" {
		if time.Since(b.CreateAt) < 3*time.Minute {
			return nil
		}
		page, e := p.Backend.Lifecycle.ListSandboxes(ctx, osb.ListOptions{Metadata: map[string]string{"platform": sandboxPlatformIdentity, "operationId": b.ID}, PageSize: 10})
		if e != nil {
			return e
		}
		if len(page.Items) == 1 {
			info := page.Items[0]
			return p.Store.Update(ctx, b.ID, info.ID, remoteState(info.Status.State), info.ExpiresAt)
		}
		// Zero matches is not proof of failure: retain Unknown and its quota reservation.
		return p.Store.Update(ctx, b.ID, "", "Unknown", nil)
	}
	if b.State == "Deleting" {
		e := p.Backend.Lifecycle.DeleteSandbox(ctx, b.BackendID)
		if e != nil && !NotFound(e) {
			return e
		}
		return p.Store.Update(ctx, b.ID, "", "Deleted", nil)
	}
	info, e := p.Backend.Lifecycle.GetSandbox(ctx, b.BackendID)
	if NotFound(e) {
		return p.Store.Update(ctx, b.ID, "", "Deleted", nil)
	}
	if e != nil {
		return e
	}
	return p.Store.Update(ctx, b.ID, info.ID, remoteState(info.Status.State), info.ExpiresAt)
}
func NotFound(e error) bool {
	var api *osb.APIError
	return errors.As(e, &api) && api.StatusCode == 404
}

// Storage outages must not block sandbox deletion retries and lifecycle reconciliation.
func (p *Platform) Reconcile(ctx context.Context) {
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); p.reconcilePreparations(ctx) }()
	if p.Storage != nil {
		wg.Add(1)
		go func() { defer wg.Done(); reconcileLoop(ctx, p.reconcileWorkspaces) }()
	}
	reconcileLoop(ctx, p.reconcileSandboxes)
	wg.Wait()
}
func reconcileLoop(ctx context.Context, run func(context.Context)) {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			run(ctx)
		}
	}
}
func (p *Platform) reconcileWorkspaces(ctx context.Context) {
	tasks, e := p.Store.WorkspaceTasks(ctx)
	if e != nil {
		logger.Error(ctx, "list workspace tasks", zap.Error(e))
		return
	}
	// A fixed worker pool bounds API pressure and DB connections, with no overlapping batches.
	jobs := make(chan domain.Workspace)
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for w := range jobs {
				op, cancel := context.WithTimeout(ctx, 20*time.Second)
				e := p.SyncWorkspace(op, w)
				cancel()
				if e != nil && ctx.Err() == nil {
					logger.Warn(ctx, "workspace sync pending", zap.String("workspaceId", w.ID), zap.Error(e))
				}
			}
		}()
	}
	for _, w := range tasks {
		select {
		case jobs <- w:
		case <-ctx.Done():
			close(jobs)
			wg.Wait()
			return
		}
	}
	close(jobs)
	wg.Wait()
}
func (p *Platform) reconcileSandboxes(ctx context.Context) {
	rows, e := p.Store.Active(ctx)
	if e != nil {
		logger.Error(ctx, "list active sandboxes", zap.Error(e))
		return
	}
	for _, b := range rows {
		if ctx.Err() != nil {
			return
		}
		op, cancel := context.WithTimeout(ctx, 10*time.Second)
		e = p.SyncOne(op, b)
		cancel()
		if e != nil {
			logger.Warn(ctx, "sandbox reconcile failed", zap.String("sandboxId", b.ID), zap.Error(e))
		}
	}
}

func remoteState(s osb.SandboxState) string {
	if string(s) == "Failed" {
		return "Error"
	}
	return string(s)
}
