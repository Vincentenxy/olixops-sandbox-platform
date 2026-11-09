package handler

import (
	"context"
	"github.com/gin-gonic/gin"
	"olixops-sandbox-platform/internal/domain"
	"olixops-sandbox-platform/pkg/response"
	"strings"
	"time"
)

func (h *PlatformHandler) workspace(c *gin.Context, id string) (domain.Workspace, bool) {
	w, e := h.P.Store.Workspace(c.Request.Context(), id)
	if e != nil {
		h.err(c, e)
		return w, false
	}
	role, ok := h.role(c, w.ProjectID)
	if !ok {
		return w, false
	}
	if !domain.CanAccess(w.OwnerID, h.user(c), role, h.admin(c)) {
		response.AbortWithHTTPStatus(c, 404)
		return w, false
	}
	return w, true
}

// Namespace godoc
// @Summary 当前用户个人逻辑 Namespace（不是 Kubernetes Namespace）
// @Tags workspaces
// @Produce json
// @Success 200 {object} response.Response
// @Security BearerAuth
// @Router /api/v1/namespace/current [get]
func (h *PlatformHandler) Namespace(c *gin.Context) {
	n, e := h.P.Store.Namespace(c.Request.Context(), h.user(c))
	if e != nil {
		h.err(c, e)
		return
	}
	response.Success(c, n)
}

// Workspaces godoc
// @Summary 按项目查询可见持久工作区
// @Tags workspaces
// @Accept json
// @Produce json
// @Success 200 {object} response.Response
// @Security BearerAuth
// @Router /api/v1/workspace/list [post]
func (h *PlatformHandler) Workspaces(c *gin.Context) {
	var q struct {
		ProjectID string `json:"projectId" binding:"required"`
	}
	if !bind(c, &q) {
		return
	}
	role, ok := h.role(c, q.ProjectID)
	if !ok {
		return
	}
	list, e := h.P.Store.Workspaces(c.Request.Context(), q.ProjectID, h.user(c), role != "member")
	if e != nil {
		h.err(c, e)
		return
	}
	response.Success(c, list)
}

// CreateWorkspace godoc
// @Summary 创建属于当前用户的持久工作区与独立 PVC
// @Tags workspaces
// @Accept json
// @Produce json
// @Success 200 {object} response.Response
// @Security BearerAuth
// @Router /api/v1/workspace/create [post]
func (h *PlatformHandler) CreateWorkspace(c *gin.Context) {
	var q struct {
		ProjectID  string `json:"projectId" binding:"required"`
		Name       string `json:"name" binding:"required,max=80"`
		RequestKey string `json:"requestKey" binding:"required,min=8,max=128"`
	}
	if !bind(c, &q) {
		return
	}
	if _, ok := h.role(c, q.ProjectID); !ok {
		return
	}
	q.Name = strings.TrimSpace(q.Name)
	if q.Name == "" {
		response.AbortWithHTTPStatus(c, 400)
		return
	}
	if h.P.Storage == nil {
		response.AbortWithHTTPStatus(c, 409)
		return
	}
	cfg := h.C.Workspace
	w, e := h.P.Store.ReserveWorkspace(c.Request.Context(), domain.Workspace{ProjectID: q.ProjectID, OwnerID: h.user(c), Name: q.Name, RequestKey: q.RequestKey, ClusterID: cfg.ClusterID, KubeNamespace: cfg.Namespace, StorageClass: cfg.StorageClass, RequestedBytes: cfg.SizeBytes}, cfg.ProjectQuota, cfg.ProjectBytes)
	if e != nil {
		h.err(c, e)
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 20*time.Second)
	defer cancel()
	// A provisioning intent is durable even if the API response is lost. The worker resumes it.
	_ = h.P.SyncWorkspace(ctx, w)
	w, e = h.P.Store.Workspace(c.Request.Context(), w.ID)
	if e != nil {
		h.err(c, e)
		return
	}
	response.Success(c, w)
}

// WorkspaceAction godoc
// @Summary 同步、归档或取消归档工作区（保留 PVC）
// @Tags workspaces
// @Accept json
// @Produce json
// @Success 200 {object} response.Response
// @Security BearerAuth
// @Router /api/v1/workspace/action [post]
func (h *PlatformHandler) WorkspaceAction(c *gin.Context) {
	var q struct {
		ID     string `json:"id" binding:"required"`
		Action string `json:"action" binding:"required,oneof=sync archive unarchive"`
	}
	if !bind(c, &q) {
		return
	}
	w, ok := h.workspace(c, q.ID)
	if !ok {
		return
	}
	var e error
	if q.Action == "sync" {
		e = h.P.SyncWorkspace(c.Request.Context(), w)
	} else {
		e = h.P.Store.ArchiveWorkspace(c.Request.Context(), w.ID, h.user(c), q.Action == "archive")
	}
	if e != nil {
		h.err(c, e)
		return
	}
	w, e = h.P.Store.Workspace(c.Request.Context(), w.ID)
	if e != nil {
		h.err(c, e)
		return
	}
	response.Success(c, w)
}
func (h *PlatformHandler) conversation(c *gin.Context, id string) (domain.Conversation, bool) {
	v, e := h.P.Store.Conversation(c.Request.Context(), id)
	if e != nil {
		h.err(c, e)
		return v, false
	}
	r, ok := h.role(c, v.ProjectID)
	if !ok {
		return v, false
	}
	if !domain.CanAccess(v.OwnerID, h.user(c), r, h.admin(c)) {
		response.AbortWithHTTPStatus(c, 404)
		return v, false
	}
	return v, true
}

// Conversations godoc
// @Summary 查询可见外部对话引用
// @Tags conversations
// @Accept json
// @Produce json
// @Success 200 {object} response.Response
// @Security BearerAuth
// @Router /api/v1/conversation/list [post]
func (h *PlatformHandler) Conversations(c *gin.Context) {
	var q struct {
		ProjectID string `json:"projectId" binding:"required"`
	}
	if !bind(c, &q) {
		return
	}
	r, ok := h.role(c, q.ProjectID)
	if !ok {
		return
	}
	v, e := h.P.Store.Conversations(c.Request.Context(), q.ProjectID, h.user(c), r != "member")
	if e != nil {
		h.err(c, e)
		return
	}
	response.Success(c, v)
}

// RegisterConversation godoc
// @Summary 幂等登记当前用户的外部对话引用，不接管聊天消息
// @Tags conversations
// @Accept json
// @Produce json
// @Success 200 {object} response.Response
// @Security BearerAuth
// @Router /api/v1/conversation/register [post]
func (h *PlatformHandler) RegisterConversation(c *gin.Context) {
	var q struct {
		ProjectID     string `json:"projectId" binding:"required"`
		IntegrationID string `json:"integrationId" binding:"required,max=80"`
		ExternalID    string `json:"externalId" binding:"required,max=255"`
		Title         string `json:"title" binding:"required,max=160"`
	}
	if !bind(c, &q) {
		return
	}
	if _, ok := h.role(c, q.ProjectID); !ok {
		return
	}
	q.IntegrationID = strings.TrimSpace(q.IntegrationID)
	q.ExternalID = strings.TrimSpace(q.ExternalID)
	q.Title = strings.TrimSpace(q.Title)
	if q.IntegrationID == "" || q.ExternalID == "" || q.Title == "" {
		response.AbortWithHTTPStatus(c, 400)
		return
	}
	v, e := h.P.Store.RegisterConversation(c.Request.Context(), domain.Conversation{ProjectID: q.ProjectID, OwnerID: h.user(c), IntegrationID: q.IntegrationID, ExternalID: q.ExternalID, Title: q.Title})
	if e != nil {
		h.err(c, e)
		return
	}
	response.Success(c, v)
}

// BindConversation godoc
// @Summary 对话绑定或解除工作区，校验归属与版本并保留历史
// @Tags conversations
// @Accept json
// @Produce json
// @Success 200 {object} response.Response
// @Security BearerAuth
// @Router /api/v1/conversation/workspace [post]
func (h *PlatformHandler) BindConversation(c *gin.Context) {
	var q struct {
		ID              string `json:"id" binding:"required"`
		WorkspaceID     string `json:"workspaceId"`
		ExpectedVersion *int64 `json:"expectedVersion" binding:"required"`
	}
	if !bind(c, &q) {
		return
	}
	v, ok := h.conversation(c, q.ID)
	if !ok {
		return
	}
	if q.WorkspaceID != "" {
		w, ok := h.workspace(c, q.WorkspaceID)
		if !ok {
			return
		}
		if w.OwnerID != v.OwnerID || w.ProjectID != v.ProjectID {
			response.AbortWithHTTPStatus(c, 404)
			return
		}
	}
	v, e := h.P.Store.BindConversation(c.Request.Context(), v.ID, q.WorkspaceID, h.user(c), *q.ExpectedVersion)
	if e != nil {
		h.err(c, e)
		return
	}
	response.Success(c, v)
}

// ResolveConversation godoc
// @Summary 定位对话的工作区、持久卷及当前实例，不自动启动或重放命令
// @Tags conversations
// @Accept json
// @Produce json
// @Success 200 {object} response.Response
// @Security BearerAuth
// @Router /api/v1/conversation/resolve [post]
func (h *PlatformHandler) ResolveConversation(c *gin.Context) {
	var q struct {
		ID string `json:"id" binding:"required"`
	}
	if !bind(c, &q) {
		return
	}
	v, ok := h.conversation(c, q.ID)
	if !ok {
		return
	}
	history, e := h.P.Store.BindingHistory(c.Request.Context(), v.ID)
	if e != nil {
		h.err(c, e)
		return
	}
	var workspace *domain.Workspace
	var sb *domain.Sandbox
	if v.WorkspaceID != "" {
		w, ok := h.workspace(c, v.WorkspaceID)
		if !ok {
			return
		}
		workspace = &w
		if w.ActiveSandboxID != "" {
			b, ok := h.authorized(c, w.ActiveSandboxID)
			if !ok {
				return
			}
			sb = &b
		}
	}
	response.Success(c, gin.H{"conversation": v, "workspace": workspace, "sandbox": sb, "history": history})
}
