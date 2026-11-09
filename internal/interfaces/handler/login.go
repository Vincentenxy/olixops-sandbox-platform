package handler

import (
	"errors"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"olixops-sandbox-platform/internal/infrastructure/localauth"
	"olixops-sandbox-platform/pkg/logger"
	"olixops-sandbox-platform/pkg/response"
	"olixops-sandbox-platform/pkg/userctx"
	"sync"
	"time"
)

type LoginHandler struct {
	Auth     *localauth.Service
	mu       sync.Mutex
	attempts map[string]attempt
}
type attempt struct {
	Count int
	Until time.Time
}

func NewLoginHandler(a *localauth.Service) *LoginHandler {
	return &LoginHandler{Auth: a, attempts: make(map[string]attempt)}
}
func (h *LoginHandler) limited(ip string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	now := time.Now()
	if len(h.attempts) > 10000 {
		for k, v := range h.attempts {
			if now.After(v.Until) {
				delete(h.attempts, k)
			}
		}
		if len(h.attempts) > 10000 {
			return true
		}
	}
	v := h.attempts[ip]
	if now.After(v.Until) {
		v = attempt{Until: now.Add(time.Minute)}
	}
	v.Count++
	h.attempts[ip] = v
	return v.Count > 10
}

// Login godoc
// @Summary 本地账号密码登录
// @Tags auth
// @Accept json
// @Produce json
// @Param request body object true "username / password"
// @Success 200 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 429 {object} response.Response
// @Router /api/v1/pub/auth/login [post]
func (h *LoginHandler) Login(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	if h.limited(c.ClientIP()) {
		c.Header("Retry-After", "60")
		response.AbortWithHTTPStatus(c, 429)
		return
	}
	var q struct {
		Username string `json:"username" binding:"required,max=64"`
		Password string `json:"password" binding:"required,max=72"`
	}
	if !bind(c, &q) {
		return
	}
	result, e := h.Auth.Login(c.Request.Context(), q.Username, q.Password)
	if e != nil {
		if errors.Is(e, localauth.ErrCredentials) {
			response.AbortWithHTTPStatus(c, 401)
		} else {
			logger.Error(c, "local login failed", zap.Error(e))
			response.AbortWithHTTPStatus(c, 500)
		}
		return
	}
	response.Success(c, result)
}

// Logout godoc
// @Summary 退出并立即撤销当前本地会话
// @Tags auth
// @Produce json
// @Success 200 {object} response.Response
// @Security BearerAuth
// @Router /api/v1/auth/logout [post]
func (h *LoginHandler) Logout(c *gin.Context) {
	u := userctx.FromContext(c)
	if e := h.Auth.Logout(c.Request.Context(), u.SessionID, u.UserID); e != nil {
		response.AbortWithHTTPStatus(c, 500)
		return
	}
	response.Success(c, nil)
}
func (h *LoginHandler) Session(c *gin.Context) {
	u := userctx.FromContext(c)
	ok, e := h.Auth.Valid(c.Request.Context(), u.SessionID, u.UserID)
	if e != nil {
		logger.Error(c, "validate session", zap.Error(e))
		response.AbortWithHTTPStatus(c, 500)
		return
	}
	if !ok {
		response.AbortWithHTTPStatus(c, 401)
		return
	}
	c.Next()
}
