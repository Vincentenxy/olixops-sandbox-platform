package handler

import (
	"context"
	"time"

	"github.com/gin-gonic/gin"
	"olixops-sandbox-platform/internal/domain"
	"olixops-sandbox-platform/pkg/response"
)

// ContinueConversation godoc
// @Summary 持久化准备对话运行；重复请求返回同一上下文
// @Tags conversations
// @Accept json
// @Produce json
// @Param body body domain.ContinueRequest true "对话版本、受控镜像、TTL 与幂等键"
// @Success 200 {object} response.Response
// @Failure 409 {object} response.Response
// @Security BearerAuth
// @Router /api/v1/conversation/continue [post]
func (h *PlatformHandler) ContinueConversation(c *gin.Context) {
	var q domain.ContinueRequest
	if !bind(c, &q) {
		return
	}
	if _, ok := h.conversation(c, q.ConversationID); !ok {
		return
	}
	allowed := false
	for _, image := range h.C.Sandbox.Images {
		if image == q.Image {
			allowed = true
		}
	}
	if !allowed || q.TTL > h.C.Sandbox.MaxTTL {
		response.AbortWithHTTPStatus(c, 400)
		return
	}
	if h.P.Storage == nil {
		h.err(c, domain.Blocked("storage_disabled", "尚未启用存储连接"))
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()
	r, e := h.P.Store.BeginRun(ctx, q, h.user(c), h.admin(c))
	if e != nil {
		h.err(c, e)
		return
	}
	response.Success(c, r)
}
func (h *PlatformHandler) runContext(c *gin.Context, id string) (domain.RunContext, bool) {
	r, e := h.P.Store.Run(c.Request.Context(), id)
	if e != nil {
		h.err(c, e)
		return r, false
	}
	if r.Actor != h.user(c) {
		response.AbortWithHTTPStatus(c, 404)
		return r, false
	}
	role, ok := h.role(c, r.ProjectID)
	if !ok {
		return r, false
	}
	if !domain.CanAccess(r.OwnerID, h.user(c), role, h.admin(c)) {
		response.AbortWithHTTPStatus(c, 404)
		return r, false
	}
	return r, true
}

type RunIDRequest struct {
	ID string `json:"id" binding:"required"`
}
type RunProjectRequest struct {
	ProjectID string `json:"projectId" binding:"required"`
}

// RunStatus godoc
// @Summary 当前操作者的执行上下文及准备进度
// @Param body body RunIDRequest true "执行上下文 ID"
// @Tags runs
// @Accept json
// @Produce json
// @Success 200 {object} response.Response
// @Security BearerAuth
// @Router /api/v1/run/status [post]
func (h *PlatformHandler) RunStatus(c *gin.Context) {
	var q RunIDRequest
	if !bind(c, &q) {
		return
	}
	r, ok := h.runContext(c, q.ID)
	if !ok {
		return
	}
	response.Success(c, r)
}

// Runs godoc
// @Summary 当前项目中由本人发起的执行与准备记录
// @Param body body RunProjectRequest true "项目 ID"
// @Tags runs
// @Accept json
// @Produce json
// @Success 200 {object} response.Response
// @Security BearerAuth
// @Router /api/v1/run/list [post]
func (h *PlatformHandler) Runs(c *gin.Context) {
	var q RunProjectRequest
	if !bind(c, &q) {
		return
	}
	role, ok := h.role(c, q.ProjectID)
	if !ok {
		return
	}
	items, e := h.P.Store.Runs(c.Request.Context(), q.ProjectID, h.user(c))
	if e != nil {
		h.err(c, e)
		return
	}
	visible := []domain.RunContext{}
	for _, r := range items {
		if domain.CanAccess(r.OwnerID, h.user(c), role, h.admin(c)) {
			visible = append(visible, r)
		}
	}
	response.Success(c, visible)
}

// ReleaseRun godoc
// @Summary 结束本次执行；保留沙箱、后台进程和 PVC
// @Param body body RunIDRequest true "执行上下文 ID"
// @Tags runs
// @Accept json
// @Produce json
// @Success 200 {object} response.Response
// @Failure 409 {object} response.Response
// @Security BearerAuth
// @Router /api/v1/run/release [post]
func (h *PlatformHandler) ReleaseRun(c *gin.Context) {
	var q RunIDRequest
	if !bind(c, &q) {
		return
	}
	r, ok := h.runContext(c, q.ID)
	if !ok {
		return
	}
	if e := h.P.Store.ReleaseRun(c.Request.Context(), r, h.user(c)); e != nil {
		h.err(c, e)
		return
	}
	r, e := h.P.Store.Run(c.Request.Context(), r.ID)
	if e != nil {
		h.err(c, e)
		return
	}
	response.Success(c, r)
}
