// Package userctx 提供认证用户信息在请求生命周期内的存取能力。
//
// 认证中间件解析 JWT 后将 User 写入 gin.Context，
// 后续 handler / service 通过 FromContext 获取当前登录用户。
package userctx

import (
	"context"

	"github.com/gin-gonic/gin"
)

// contextKey 自定义 context key 类型，避免冲突
type contextKey string

// userKey 是 context 中存储用户信息的 key
const userKey contextKey = "authUser"

// User 认证用户信息（对应 JWT claims 解析结果）
type User struct {
	Cookie    string `json:"-"`
	SessionID string `json:"-"`
	UserID    string `json:"userId"` // 用户 ID（claims.staffuserid）
	Name      string `json:"name"`   // 用户姓名（claims.name）
	Jwt       string `json:"-"`      // 原始 JWT token（用于向下游服务透传）
}

// Set 将用户信息写入 gin.Context
func Set(c *gin.Context, u *User) {
	c.Set(string(userKey), u)
}

// FromContext 从 context 中获取用户信息，支持 gin.Context 和标准 context.Context
// 未认证或解析失败时返回 nil
func FromContext(ctx context.Context) *User {
	if ctx == nil {
		return nil
	}

	var v any
	if gc, ok := ctx.(*gin.Context); ok {
		v, _ = gc.Get(string(userKey))
	} else {
		v = ctx.Value(userKey)
	}

	if u, ok := v.(*User); ok {
		return u
	}
	return nil
}

// MustFromContext 从 context 中获取用户信息，未认证时返回 false
// 用于认证接口中必须存在用户的场景
func MustFromContext(ctx context.Context) (*User, bool) {
	u := FromContext(ctx)
	return u, u != nil
}
