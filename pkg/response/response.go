package response

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// Response 统一响应结构，JSON 字段为小驼峰
type Response struct {
	Code    int    `json:"code"` // 0-成功；-1-通用失败；1-1000 系统预留（600 以内与 HTTP 状态码一致）；10000+ 业务失败
	Message string `json:"msg"`  // 提示信息
	Data    any    `json:"data"` // 业务数据
}

// Success 成功响应：code=0
func Success(c *gin.Context, data any) {
	c.JSON(http.StatusOK, Response{
		Code:    0,
		Message: "success",
		Data:    data,
	})
}

// Fail 业务失败响应：HTTP 状态码 200，code 为业务错误码
func Fail(c *gin.Context, code int, msg string) {
	c.JSON(http.StatusOK, Response{
		Code:    code,
		Message: msg,
		Data:    nil,
	})
}

// Error 通用失败响应：code=-1
func Error(c *gin.Context, msg string) {
	Fail(c, -1, msg)
}

// AbortWithHTTPStatus HTTP 错误响应（硬性要求）：
// HTTP 状态码与 body 内的 code、msg 保持对应，msg 使用标准 HTTP 状态文本。
// 用于 400/401/403/404/500 等场景。
func AbortWithHTTPStatus(c *gin.Context, httpStatus int) {
	c.AbortWithStatusJSON(httpStatus, Response{
		Code:    httpStatus,
		Message: http.StatusText(httpStatus),
		Data:    nil,
	})
}
