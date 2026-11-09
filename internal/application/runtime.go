package application

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	osb "github.com/alibaba/OpenSandbox/sdks/sandbox/go"
	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"
	"olixops-agent-platform/internal/domain"
	"olixops-agent-platform/internal/infrastructure/store"
	"olixops-agent-platform/pkg/logger"
)

func (p *Platform) actorIsAdmin(actor string) bool {
	for _, id := range p.Admins {
		if actor == id {
			return true
		}
	}
	return false
}

// ProcessNextPreparation is shared by the durable worker and integration acceptance tests.
func (p *Platform) ProcessNextPreparation(ctx context.Context, worker string) (bool, error) {
	r, e := p.Store.ClaimPreparation(ctx, worker)
	if errors.Is(e, pgx.ErrNoRows) {
		return false, nil
	}
	if e != nil {
		return false, e
	}
	op, cancel := context.WithTimeout(ctx, 60*time.Second)
	state, reason, message := p.prepareRun(op, r)
	cancel()
	save, done := context.WithTimeout(context.Background(), 5*time.Second)
	defer done()
	e = p.Store.FinishPreparation(save, r, state, reason, message)
	if errors.Is(e, store.ErrLeaseLost) {
		return true, nil
	}
	return true, e
}
func (p *Platform) prepareRun(ctx context.Context, r domain.RunContext) (string, string, string) {
	if e := p.Store.RunActorAllowed(ctx, r, p.actorIsAdmin(r.Actor)); e != nil {
		if errors.Is(e, pgx.ErrNoRows) {
			return "Failed", "permission_revoked", "当前操作者已无权限，本次执行失效"
		}
		return "Waiting", "authorization_unavailable", "暂时无法核对权限，稍后重试"
	}
	if r.SandboxID != "" {
		b, err := p.Store.Get(ctx, r.SandboxID)
		if err != nil {
			return "Waiting", "runtime_unavailable", "无法读取固定的运行实例"
		}
		if domain.Terminal(b.State) {
			// The old context can never execute again. The workspace's separate
			// consumer reservation still prevents premature replacement.
			return "Invalidated", "runtime_stopped", "原运行实例已结束，请重新继续对话"
		}
	}
	if p.Storage == nil {
		return "Waiting", "storage_disabled", "存储连接未启用，等待管理员恢复配置"
	}
	w, e := p.Store.Workspace(ctx, r.WorkspaceID)
	if e != nil {
		return "Waiting", "workspace_unavailable", "无法读取工作区，稍后核对"
	}
	// Reconcile any previous compute consumer before admitting a replacement.
	if w.ActiveSandboxID != "" {
		b, err := p.Store.Get(ctx, w.ActiveSandboxID)
		if err != nil {
			return "Waiting", "runtime_unavailable", "无法读取运行实例"
		}
		if b.BackendID != "" {
			if err = p.SyncOne(ctx, b); err != nil {
				return "Waiting", "runtime_unavailable", "暂时无法核对沙箱状态"
			}
		}
	}
	if e = p.SyncWorkspace(ctx, w); e != nil {
		return "Waiting", "storage_unavailable", "存储核对未通过；请检查集群连接、PVC 身份和权限"
	}
	w, e = p.Store.Workspace(ctx, w.ID)
	if e != nil {
		return "Waiting", "workspace_unavailable", "无法读取最新工作区"
	}
	if r.SandboxID == "" && w.ActiveSandboxID != "" {
		old, err := p.Store.Get(ctx, w.ActiveSandboxID)
		if err != nil {
			return "Waiting", "runtime_unavailable", "无法读取旧实例"
		}
		if domain.Terminal(old.State) || old.State == "Deleting" {
			return "Waiting", "consumer_exiting", "旧实例或存储消费者尚未完全退出，请等待核对"
		}
	}
	if w.ActiveSandboxID == "" {
		used, err := p.Storage.InUse(ctx, w)
		if err != nil {
			return "Waiting", "consumers_unavailable", "暂时无法核对卷消费者"
		}
		if used {
			return "Waiting", "external_consumer", "仍有 Pod 或 BatchSandbox 使用该卷，暂不能创建实例"
		}
	}
	b, e := p.Store.ReserveRunSandbox(ctx, r, p.actorIsAdmin(r.Actor))
	if e != nil {
		if errors.Is(e, store.ErrLeaseLost) {
			return "Waiting", "lease_changed", "任务已由其他 worker 接管"
		}
		if errors.Is(e, store.ErrQuota) {
			return "Waiting", "quota_exceeded", "项目计算配额已满，释放配额后自动重试"
		}
		var conflict *domain.Conflict
		if errors.As(e, &conflict) {
			if conflict.Reason == "operation_in_progress" {
				// A health check must not suspend an otherwise valid context while
				// its own foreground command holds the execution lock.
				if r.State == "Ready" {
					return "Ready", "", "运行已就绪；当前工具操作结束后继续核对"
				}
				return "Waiting", conflict.Reason, conflict.Message
			}
			return "Failed", conflict.Reason, conflict.Message
		}
		if errors.Is(e, pgx.ErrNoRows) {
			return "Failed", "permission_revoked", "绑定或权限已变化，请重新继续"
		}
		return "Waiting", "admission_unavailable", "暂时无法准备实例，请稍后核对"
	}
	current, e := p.Store.Run(ctx, r.ID)
	if e != nil {
		return "Waiting", "task_unavailable", "无法读取准备任务"
	}
	// Refresh only resource snapshots; keep the original lease generation for every fenced write.
	r.SandboxID = current.SandboxID
	r.AttachmentGeneration = current.AttachmentGeneration
	if domain.Terminal(b.State) {
		return "Invalidated", "runtime_stopped", "原运行实例已结束，请重新继续对话"
	}
	if b.BackendID == "" && current.Operation.OwnsCreation && current.Operation.CreateSentAt == nil {
		sent, err := p.Store.MarkCreateSent(ctx, r)
		if err != nil {
			return "Waiting", "create_not_confirmed", "尚未确认创建发送状态，等待核对"
		}
		if sent {
			state, backend, expires, err := p.createRemote(ctx, b)
			if saveErr := p.Store.SavePreparedSandbox(ctx, r, backend, state, expires); saveErr != nil {
				return "Waiting", "create_not_confirmed", "创建结果尚未落库，等待远端核对"
			}
			if err != nil && state == "Failed" {
				return "Failed", "create_rejected", "OpenSandbox 拒绝创建，请检查镜像和运行配置后重新继续"
			}
			if err != nil {
				return "Waiting", "create_unknown", "创建结果不确定；将核对远端记录，不会重复发送创建"
			}
			b, e = p.Store.Get(ctx, b.ID)
			if e != nil {
				return "Waiting", "runtime_unavailable", "无法读取创建结果"
			}
		}
	}
	if b.BackendID == "" {
		page, err := p.Backend.Lifecycle.ListSandboxes(ctx, osb.ListOptions{Metadata: map[string]string{"platform": "olixops-agent-platform", "operationId": b.ID}, PageSize: 10})
		if err != nil || len(page.Items) != 1 {
			return "Waiting", "create_unknown", "尚未找到唯一的远端创建结果；保留占用并继续核对"
		}
		info := page.Items[0]
		if err = p.Store.SavePreparedSandbox(ctx, r, info.ID, remoteState(info.Status.State), info.ExpiresAt); err != nil {
			return "Waiting", "create_not_confirmed", "等待保存已发现的实例"
		}
		b, e = p.Store.Get(ctx, b.ID)
		if e != nil {
			return "Waiting", "runtime_unavailable", "无法读取已发现实例"
		}
	}
	if e = p.SyncOne(ctx, b); e != nil {
		return "Waiting", "runtime_unavailable", "暂时无法核对远端运行状态"
	}
	b, e = p.Store.Get(ctx, b.ID)
	if e != nil {
		return "Waiting", "runtime_unavailable", "无法读取运行状态"
	}
	if domain.Terminal(b.State) {
		return "Invalidated", "runtime_stopped", "原运行实例已结束，请重新继续对话"
	}
	if b.State != "Running" {
		return "Waiting", "runtime_not_ready", fmt.Sprintf("实例当前为 %s，等待就绪；可在实例页查看或停止", b.State)
	}
	ex, e := p.Backend.Exec(ctx, b.BackendID)
	if e != nil {
		return "Waiting", "exec_not_ready", "正在等待沙箱执行服务"
	}
	if e = ex.Ping(ctx); e != nil {
		return "Waiting", "exec_not_ready", "执行服务暂不可用，稍后自动重试"
	}
	return "Ready", "", "运行已就绪，后续操作已固定到原工作区"
}

func (p *Platform) reconcilePreparations(ctx context.Context) {
	worker := store.ID()
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		var wg sync.WaitGroup
		for i := 0; i < 4; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, e := p.ProcessNextPreparation(ctx, worker)
				if e != nil && ctx.Err() == nil {
					logger.Warn(ctx, "prepare conversation runtime", zap.Error(e))
				}
			}()
		}
		wg.Wait()
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
