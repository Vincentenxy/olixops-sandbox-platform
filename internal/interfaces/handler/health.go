package handler

import (
	"github.com/gin-gonic/gin"

	"olixops-agent-platform/pkg/response"
)

// HealthHandler 健康检查处理器
type HealthHandler struct{}

// NewHealthHandler 创建健康检查处理器
func NewHealthHandler() *HealthHandler {
	return &HealthHandler{}
}

// Ping 健康检查接口
func (h *HealthHandler) Ping(c *gin.Context) {
	response.Success(c, gin.H{"status": "ok"})
}
