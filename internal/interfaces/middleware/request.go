package middleware

import (
	"crypto/rand"
	"encoding/hex"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"olixops-sandbox-platform/pkg/logger"
)

// RequestIDHeader HTTP 请求头中的 request-id key
const RequestIDHeader = "X-Request-Id"

// RequestID 中间件：
//   - 请求头携带 x-request-id 时透传，未携带时自动生成
//   - 将 trace-id 注入 gin.Context，供日志模块使用
//   - 响应头回写 x-request-id，便于调用方排查
func RequestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		traceID := c.GetHeader(RequestIDHeader)
		if traceID == "" {
			traceID = generateID()
		}
		c.Set(string(logger.TraceIDKey), traceID)
		c.Header(RequestIDHeader, traceID)
		c.Next()
	}
}

// GinLogger 替换 gin 默认 Logger 中间件，使用统一日志模块输出访问日志
func GinLogger() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		logger.Info(c, "http access",
			zap.String("method", c.Request.Method),
			zap.String("path", c.Request.URL.Path),
			zap.Int("status", c.Writer.Status()),
			zap.Duration("cost", time.Since(start)),
			zap.String("clientIp", c.ClientIP()),
		)
	}
}

// generateID 生成 16 字节随机十六进制 trace-id
func generateID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return ""
	}
	return hex.EncodeToString(b)
}
