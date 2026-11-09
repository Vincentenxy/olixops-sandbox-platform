package application

import (
	"context"
	"olixops-agent-platform/internal/domain"
	"time"
)

// Creating is a durable provisioning intent. Only never-observed PVCs can be provisioned.
func (p *Platform) SyncWorkspace(ctx context.Context, w domain.Workspace) error {
	if p.Storage == nil {
		return nil
	}
	// Callers may hold old snapshots. Never provision from them: lock and reread first.
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	w, e := p.Store.SyncWorkspaceStorage(ctx, w.ID, func(current *domain.Workspace) error {
		o, err := p.Storage.Ensure(ctx, *current)
		if err != nil {
			if current.PVCUID != "" || o.UID != "" {
				current.State = "Error"
			}
			current.StoragePhase = "Unknown"
			current.Message = "存储核对失败，请检查集群连接、PVC 身份或权限"
			return err
		}
		current.PVCUID, current.PVName, current.StoragePhase = o.UID, o.PV, o.Phase
		current.State = "Available"
		current.Message = "持久数据已就绪；停止运行不会删除数据"
		if o.Phase == "Pending" {
			current.Message = "等待首次使用或存储供应，可启动首个沙箱"
		}
		return nil
	})
	if e != nil {
		return e
	}
	if w.ActiveSandboxID != "" {
		b, e := p.Store.Get(ctx, w.ActiveSandboxID)
		if e != nil {
			return e
		}
		if domain.Terminal(b.State) {
			if b.BackendID != "" {
				_, e = p.Backend.Lifecycle.GetSandbox(ctx, b.BackendID)
				if e == nil {
					return nil
				}
				if !NotFound(e) {
					return e
				}
			}
			used, e := p.Storage.InUse(ctx, w)
			if e != nil {
				return e
			}
			if !used {
				return p.Store.ReleaseWorkspace(ctx, w.ID, b.ID)
			}
		}
	}
	return nil
}
