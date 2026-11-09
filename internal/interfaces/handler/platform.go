package handler

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	osb "github.com/alibaba/OpenSandbox/sdks/sandbox/go"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"
	"io"
	"net/http"
	"net/url"
	"olixops-agent-platform/internal/application"
	"olixops-agent-platform/internal/domain"
	"olixops-agent-platform/internal/infrastructure/config"
	"olixops-agent-platform/internal/infrastructure/sandbox"
	"olixops-agent-platform/internal/infrastructure/store"
	"olixops-agent-platform/pkg/logger"
	"olixops-agent-platform/pkg/response"
	"olixops-agent-platform/pkg/userctx"
	"path"
	"strconv"
	"strings"
	"time"
)

type PlatformHandler struct {
	P *application.Platform
	C *config.Config
}

func (h *PlatformHandler) user(c *gin.Context) string { return userctx.FromContext(c).UserID }
func (h *PlatformHandler) admin(c *gin.Context) bool {
	for _, u := range h.C.Auth.Admins {
		if u == h.user(c) {
			return true
		}
	}
	return false
}
func (h *PlatformHandler) role(c *gin.Context, p string) (string, bool) {
	r, e := h.P.Store.Role(c.Request.Context(), p, h.user(c))
	if h.admin(c) {
		return "admin", true
	}
	if e != nil {
		if !errors.Is(e, pgx.ErrNoRows) {
			h.err(c, e)
		} else {
			response.AbortWithHTTPStatus(c, 404)
		}
		return "", false
	}
	return r, true
}
func (h *PlatformHandler) err(c *gin.Context, e error) {
	logger.Error(c, "platform request failed", zap.Error(e))
	code := 500
	var api *osb.APIError
	if errors.As(e, &api) {
		code = 502
	}
	if errors.Is(e, store.ErrQuota) || errors.Is(e, store.ErrConflict) {
		code = 409
	}
	if errors.Is(e, pgx.ErrNoRows) {
		code = 404
	}
	if errors.Is(e, sandbox.ErrResponseTooLarge) {
		code = 413
	}
	var conflict *domain.Conflict
	if errors.As(e, &conflict) {
		c.AbortWithStatusJSON(409, response.Response{Code: 409, Message: http.StatusText(409), Data: conflict})
		return
	}
	response.AbortWithHTTPStatus(c, code)
}
func bind(c *gin.Context, v any) bool {
	if e := c.ShouldBindJSON(v); e != nil {
		response.AbortWithHTTPStatus(c, 400)
		return false
	}
	return true
}

// Profile godoc
// @Summary 当前用户
// @Tags platform
// @Produce json
// @Success 200 {object} response.Response
// @Security BearerAuth
// @Router /api/v1/user/profile [get]
func (h *PlatformHandler) Profile(c *gin.Context) {
	u := userctx.FromContext(c)
	response.Success(c, gin.H{"userId": u.UserID, "name": u.Name, "admin": h.admin(c)})
}

// Settings godoc
// @Summary 模板配置
// @Tags platform
// @Produce json
// @Success 200 {object} response.Response
// @Security BearerAuth
// @Router /api/v1/settings [get]
func (h *PlatformHandler) Settings(c *gin.Context) {
	response.Success(c, gin.H{"images": h.C.Sandbox.Images, "maxTtl": h.C.Sandbox.MaxTTL, "cpu": "1", "memory": "1Gi", "workspaceEnabled": h.P.Storage != nil, "workspaceBytes": h.C.Workspace.SizeBytes, "workspaceQuota": h.C.Workspace.ProjectQuota})
}

// Projects godoc
// @Summary 查询项目
// @Tags platform
// @Produce json
// @Success 200 {object} response.Response
// @Security BearerAuth
// @Router /api/v1/projects [get]
func (h *PlatformHandler) Projects(c *gin.Context) {
	v, e := h.P.Store.Projects(c.Request.Context(), h.user(c), h.admin(c))
	if e != nil {
		h.err(c, e)
		return
	}
	response.Success(c, v)
}

// CreateProject godoc
// @Summary 创建项目并将当前用户设为 owner
// @Tags projects
// @Accept json
// @Produce json
// @Success 200 {object} response.Response
// @Security BearerAuth
// @Router /api/v1/project/create [post]
func (h *PlatformHandler) CreateProject(c *gin.Context) {
	var q struct {
		Name string `json:"name" binding:"required,min=1,max=80"`
	}
	if !bind(c, &q) {
		return
	}
	q.Name = strings.TrimSpace(q.Name)
	if q.Name == "" {
		response.AbortWithHTTPStatus(c, 400)
		return
	}
	id, e := h.P.Store.CreateProject(c.Request.Context(), h.user(c), q.Name, h.C.Sandbox.DefaultQuota)
	if e != nil {
		h.err(c, e)
		return
	}
	response.Success(c, gin.H{"id": id})
}

// Members godoc
// @Summary 项目成员
// @Tags platform
// @Produce json
// @Success 200 {object} response.Response
// @Security BearerAuth
// @Router /api/v1/project/members [post]
func (h *PlatformHandler) Members(c *gin.Context) {
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
	if r != "owner" && r != "admin" && r != "maintainer" {
		response.AbortWithHTTPStatus(c, 403)
		return
	}
	v, e := h.P.Store.Members(c.Request.Context(), q.ProjectID)
	if e != nil {
		h.err(c, e)
		return
	}
	response.Success(c, v)
}

// SetMember godoc
// @Summary 设置成员
// @Tags platform
// @Produce json
// @Success 200 {object} response.Response
// @Security BearerAuth
// @Router /api/v1/project/member/set [post]
func (h *PlatformHandler) SetMember(c *gin.Context) {
	var q struct {
		ProjectID string `json:"projectId" binding:"required"`
		UserID    string `json:"userId" binding:"required,max=128"`
		Role      string `json:"role" binding:"required,oneof=member maintainer"`
	}
	if !bind(c, &q) {
		return
	}
	r, ok := h.role(c, q.ProjectID)
	if !ok {
		return
	}
	if r != "owner" && r != "admin" {
		response.AbortWithHTTPStatus(c, 403)
		return
	}
	if e := h.P.Store.SetMember(c.Request.Context(), q.ProjectID, q.UserID, q.Role, h.user(c)); e != nil {
		if errors.Is(e, store.ErrNotFound) {
			response.AbortWithHTTPStatus(c, 409)
		} else {
			h.err(c, e)
		}
		return
	}
	response.Success(c, nil)
}

// List godoc
// @Summary 可见沙箱
// @Tags platform
// @Produce json
// @Success 200 {object} response.Response
// @Security BearerAuth
// @Router /api/v1/sandbox/list [post]
func (h *PlatformHandler) List(c *gin.Context) {
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
	v, e := h.P.Store.List(c.Request.Context(), q.ProjectID, h.user(c), r != "member")
	if e != nil {
		h.err(c, e)
		return
	}
	response.Success(c, v)
}

// Create godoc
// @Summary 创建沙箱
// @Tags platform
// @Produce json
// @Success 200 {object} response.Response
// @Security BearerAuth
// @Router /api/v1/sandbox/create [post]
func (h *PlatformHandler) Create(c *gin.Context) {
	var q struct {
		WorkspaceID string `json:"workspaceId"`
		ProjectID   string `json:"projectId" binding:"required"`
		Name        string `json:"name" binding:"required,min=1,max=80"`
		Image       string `json:"image" binding:"required"`
		RequestKey  string `json:"requestKey" binding:"required,min=8,max=128"`
		TTL         int    `json:"ttl" binding:"min=60"`
	}
	if !bind(c, &q) {
		return
	}
	if _, ok := h.role(c, q.ProjectID); !ok {
		return
	}
	owner := h.user(c)
	if q.WorkspaceID != "" {
		w, ok := h.workspace(c, q.WorkspaceID)
		if !ok {
			return
		}
		if w.ProjectID != q.ProjectID {
			response.AbortWithHTTPStatus(c, 404)
			return
		}
		owner = w.OwnerID
	}
	allowed := false
	for _, i := range h.C.Sandbox.Images {
		if i == q.Image {
			allowed = true
		}
	}
	if !allowed || q.TTL > h.C.Sandbox.MaxTTL {
		response.AbortWithHTTPStatus(c, 400)
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 120*time.Second)
	defer cancel()
	b, e := h.P.Create(ctx, domain.Sandbox{ProjectID: q.ProjectID, OwnerID: owner, WorkspaceID: q.WorkspaceID, Name: q.Name, Image: q.Image, TTL: q.TTL, RequestKey: q.RequestKey}, h.user(c))
	if e != nil {
		if b.ID != "" && !errors.Is(e, store.ErrConflict) {
			response.Success(c, gin.H{"sandbox": b, "uncertain": b.State == "Unknown", "message": "创建未确认，请按当前记录刷新，不要重复提交"})
			return
		}
		h.err(c, e)
		return
	}
	response.Success(c, gin.H{"sandbox": b})
}
func (h *PlatformHandler) authorized(c *gin.Context, id string) (domain.Sandbox, bool) {
	b, e := h.P.Store.Get(c.Request.Context(), id)
	if e != nil {
		h.err(c, e)
		return b, false
	}
	r, ok := h.role(c, b.ProjectID)
	if !ok {
		return b, false
	}
	if !domain.CanAccess(b.OwnerID, h.user(c), r, h.admin(c)) {
		response.AbortWithHTTPStatus(c, 404)
		return b, false
	}
	return b, true
}

// Action godoc
// @Summary 操作沙箱（每次执行对象级权限校验）
// @Tags sandboxes
// @Accept json
// @Produce json
// @Success 200 {object} response.Response
// @Security BearerAuth
// @Router /api/v1/sandbox/action [post]
func (h *PlatformHandler) Action(c *gin.Context) {
	var q struct {
		ID           string `json:"id" binding:"required"`
		RunContextID string `json:"runContextId"`
		Action       string `json:"action" binding:"required,oneof=sync delete renew command logs files read write remove preview"`
		Command      string `json:"command"`
		CommandID    string `json:"commandId"`
		Cursor       *int64 `json:"cursor" binding:"omitempty,min=0"`
		Path         string `json:"path"`
		Content      string `json:"content"`
		TTL          int    `json:"ttl"`
		Port         int    `json:"port"`
		Background   bool   `json:"background"`
	}
	if !bind(c, &q) {
		return
	}
	b, ok := h.authorized(c, q.ID)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 75*time.Second)
	defer cancel()
	dataOperation := q.Action != "sync" && q.Action != "delete" && q.Action != "renew"
	release, e := h.P.Store.GuardSandbox(ctx, b, q.RunContextID, h.user(c), dataOperation)
	if e != nil {
		h.err(c, e)
		return
	}
	defer release()
	if e := h.P.Store.AuditRunAction(ctx, b, q.RunContextID, q.Action, h.user(c), "requested"); e != nil {
		h.err(c, e)
		return
	}
	var result any
	e = nil
	switch q.Action {
	case "sync":
		e = h.P.SyncOne(ctx, b)
		if e == nil {
			result, e = h.P.Store.Get(ctx, b.ID)
		}
	case "delete":
		if b.BackendID == "" && !domain.Terminal(b.State) {
			response.AbortWithHTTPStatus(c, 409)
			return
		}
		if !domain.Terminal(b.State) {
			e = h.P.Store.Update(ctx, b.ID, "", "Deleting", nil)
			if e == nil {
				b.State = "Deleting"
				e = h.P.SyncOne(ctx, b)
			}
		}
	case "renew":
		if q.TTL < 60 || q.TTL > h.C.Sandbox.MaxTTL || b.BackendID == "" || domain.Terminal(b.State) {
			response.AbortWithHTTPStatus(c, 400)
			return
		}
		_, e = h.P.Backend.Lifecycle.RenewExpiration(ctx, b.BackendID, time.Now().Add(time.Duration(q.TTL)*time.Second))
		if e == nil {
			e = h.P.SyncOne(ctx, b)
		}
	default:
		if b.BackendID == "" || domain.Terminal(b.State) || b.State == "Deleting" {
			response.AbortWithHTTPStatus(c, 409)
			return
		}
		var ex *osb.ExecdClient
		ex, e = h.P.Backend.Exec(ctx, b.BackendID)
		if e != nil {
			break
		}
		switch q.Action {
		case "command":
			if strings.TrimSpace(q.Command) == "" || len(q.Command) > 16384 {
				response.AbortWithHTTPStatus(c, 400)
				return
			}
			events := []gin.H{}
			size := 0
			e = ex.RunCommand(ctx, osb.RunCommandRequest{Command: q.Command, Timeout: 60000, Background: q.Background}, func(event osb.StreamEvent) error {
				size += len(event.Data)
				if size > 1<<20 {
					return sandbox.ErrResponseTooLarge
				}
				events = append(events, gin.H{"event": event.Event, "data": event.Data, "id": event.ID})
				return nil
			})
			result = events
		case "logs":
			if q.CommandID == "" {
				response.AbortWithHTTPStatus(c, 400)
				return
			}
			var logs *osb.CommandLogsResponse
			logs, e = ex.GetCommandLogs(ctx, q.CommandID, q.Cursor)
			if e == nil {
				result = gin.H{"output": logs.Output, "cursor": logs.Cursor}
			}
		case "files", "read", "write", "remove":
			if q.Path == "" {
				q.Path = "/workspace"
			}
			q.Path = path.Clean(q.Path)
			if !strings.HasPrefix(q.Path, "/") || strings.ContainsRune(q.Path, 0) {
				response.AbortWithHTTPStatus(c, 400)
				return
			}
			switch q.Action {
			case "files":
				var files []osb.FileInfo
				files, e = ex.ListDirectory(ctx, q.Path)
				entries := []gin.H{}
				for _, file := range files {
					entries = append(entries, gin.H{"path": file.Path, "type": file.Type, "size": file.Size, "modifiedAt": file.ModifiedAt, "createdAt": file.CreatedAt, "owner": file.Owner, "group": file.Group, "mode": file.Mode})
				}
				result = entries
			case "read":
				var reader io.ReadCloser
				reader, e = ex.DownloadFile(ctx, q.Path, "")
				if e == nil {
					defer reader.Close()
					var data []byte
					data, e = io.ReadAll(io.LimitReader(reader, (4<<20)+1))
					if len(data) > 4<<20 {
						response.AbortWithHTTPStatus(c, 413)
						return
					}
					result = gin.H{"content": base64.StdEncoding.EncodeToString(data), "encoding": "base64"}
				}
			case "write":
				data, err := base64.StdEncoding.DecodeString(q.Content)
				if err != nil || len(data) > 4<<20 {
					response.AbortWithHTTPStatus(c, 400)
					return
				}
				e = ex.UploadFile(ctx, bytes.NewReader(data), osb.UploadFileOptions{FileName: path.Base(q.Path), Metadata: osb.FileMetadata{Path: q.Path}})
			case "remove":
				if q.Path == "/" {
					response.AbortWithHTTPStatus(c, 400)
					return
				}
				e = ex.DeleteFiles(ctx, []string{q.Path})
			}
		case "preview":
			if q.Port < 1024 || q.Port > 65535 || q.Port == 44772 || q.Port == 18080 {
				response.AbortWithHTTPStatus(c, 400)
				return
			}
			rel, err := url.Parse(q.Path)
			if err != nil || rel.IsAbs() || rel.Host != "" || strings.HasPrefix(q.Path, "//") || strings.ContainsAny(rel.Path, "%\\") || strings.Contains("/"+rel.Path+"/", "/../") {
				response.AbortWithHTTPStatus(c, 400)
				return
			}
			target := h.P.Backend.ProxyURL(b.BackendID, q.Port) + "/" + strings.TrimLeft((&url.URL{Path: path.Clean("/" + rel.Path)}).EscapedPath(), "/")
			if rel.RawQuery != "" {
				target += "?" + rel.RawQuery
			}
			var req *http.Request
			req, e = http.NewRequestWithContext(ctx, "GET", target, nil)
			if e != nil {
				break
			}
			req.Header.Set("OPEN-SANDBOX-API-KEY", h.P.Backend.APIKey)
			var resp *http.Response
			resp, e = h.P.Backend.HTTP.Do(req)
			if e == nil {
				defer resp.Body.Close()
				var data []byte
				data, e = io.ReadAll(io.LimitReader(resp.Body, (2<<20)+1))
				if len(data) > 2<<20 {
					response.AbortWithHTTPStatus(c, 413)
					return
				}
				result = gin.H{"status": resp.StatusCode, "contentType": resp.Header.Get("Content-Type"), "body": string(data), "port": strconv.Itoa(q.Port)}
			}
		}
	}
	if e == nil && b.WorkspaceID != "" && (q.Action == "delete" || q.Action == "sync") {
		if w, err := h.P.Store.Workspace(ctx, b.WorkspaceID); err == nil {
			e = h.P.SyncWorkspace(ctx, w)
		} else {
			e = err
		}
	}
	outcome := "success"
	if e != nil {
		outcome = "failed"
	}
	auditCtx, done := context.WithTimeout(context.Background(), 3*time.Second)
	defer done()
	if ae := h.P.Store.AuditRunAction(auditCtx, b, q.RunContextID, q.Action, h.user(c), outcome); ae != nil {
		logger.Error(c, "write audit outcome failed", zap.Error(ae))
	}
	if e != nil {
		h.err(c, e)
		return
	}
	response.Success(c, result)
}

// Audits godoc
// @Summary 审计记录
// @Tags platform
// @Produce json
// @Success 200 {object} response.Response
// @Security BearerAuth
// @Router /api/v1/audit/list [post]
func (h *PlatformHandler) Audits(c *gin.Context) {
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
	v, e := h.P.Store.Audits(c.Request.Context(), q.ProjectID, h.user(c), r != "member")
	if e != nil {
		h.err(c, e)
		return
	}
	response.Success(c, v)
}
