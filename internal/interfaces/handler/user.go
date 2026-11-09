package handler

import (
	"github.com/gin-gonic/gin"

	"olixops-agent-platform/pkg/response"
	"olixops-agent-platform/pkg/userctx"
)

// UserHandler 用户相关处理器（当前仅提供认证信息验证接口，后续业务开发时扩展）
type UserHandler struct{}

// NewUserHandler 创建用户处理器
func NewUserHandler() *UserHandler {
	return &UserHandler{}
}

// Profile 返回当前认证用户信息，用于验证认证链路
func (h *UserHandler) Profile(c *gin.Context) {
	user, ok := userctx.MustFromContext(c)
	if !ok {
		response.AbortWithHTTPStatus(c, 401)
		return
	}
	response.Success(c, user)
}
