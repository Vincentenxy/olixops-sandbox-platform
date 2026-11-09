package router

import (
	"context"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"olixops-sandbox-platform/internal/application"
	"olixops-sandbox-platform/internal/infrastructure/config"
	"olixops-sandbox-platform/internal/infrastructure/localauth"
	"olixops-sandbox-platform/internal/interfaces/handler"
	"olixops-sandbox-platform/internal/interfaces/middleware"
	"olixops-sandbox-platform/pkg/logger"
	"olixops-sandbox-platform/pkg/response"
	"time"
)

func New(cfg *config.Config, p *application.Platform) (*gin.Engine, error) {
	gin.SetMode(cfg.Server.Mode)
	r := gin.New()
	_ = r.SetTrustedProxies(nil)
	r.Use(middleware.RequestID(), middleware.GinLogger(), gin.CustomRecovery(func(c *gin.Context, recovered any) {
		logger.Error(c, "request panic", zap.Any("panic", recovered))
		response.AbortWithHTTPStatus(c, 500)
	}))
	r.Use(func(c *gin.Context) {
		c.Request.Body = httpMax(c)
		c.Header("X-Content-Type-Options", "nosniff")
		c.Next()
	})
	var login *handler.LoginHandler
	if cfg.Auth.Local.Enabled {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		local, e := localauth.New(ctx, cfg, p.Store)
		cancel()
		if e != nil {
			return nil, e
		}
		login = handler.NewLoginHandler(local)
	}
	auth, e := middleware.Auth(cfg.Auth.JwtPublicKey, cfg.Auth.Issuer, cfg.Auth.Audience)
	if e != nil {
		return nil, e
	}
	h := &handler.PlatformHandler{P: p, C: cfg}
	v := r.Group("/api/v1")
	v.GET("/pub/health", handler.NewHealthHandler().Ping)
	if login != nil {
		v.POST("/pub/auth/login", login.Login)
	}
	a := v.Group("", auth)
	if login != nil {
		a.Use(login.Session)
		a.POST("/auth/logout", login.Logout)
	}
	a.GET("/namespace/current", h.Namespace)
	a.POST("/workspace/list", h.Workspaces)
	a.POST("/workspace/create", h.CreateWorkspace)
	a.POST("/workspace/action", h.WorkspaceAction)
	a.POST("/conversation/list", h.Conversations)
	a.POST("/conversation/register", h.RegisterConversation)
	a.POST("/conversation/workspace", h.BindConversation)
	a.POST("/conversation/resolve", h.ResolveConversation)
	a.POST("/conversation/continue", h.ContinueConversation)
	a.POST("/run/status", h.RunStatus)
	a.POST("/run/list", h.Runs)
	a.POST("/run/release", h.ReleaseRun)
	a.GET("/user/profile", h.Profile)
	a.GET("/settings", h.Settings)
	a.GET("/projects", h.Projects)
	a.POST("/project/create", h.CreateProject)
	a.POST("/project/members", h.Members)
	a.POST("/project/member/set", h.SetMember)
	a.POST("/sandbox/list", h.List)
	a.POST("/sandbox/create", h.Create)
	a.POST("/sandbox/action", h.Action)
	a.POST("/audit/list", h.Audits)
	r.NoRoute(func(c *gin.Context) { response.AbortWithHTTPStatus(c, 404) })
	r.HandleMethodNotAllowed = true
	r.NoMethod(func(c *gin.Context) { response.AbortWithHTTPStatus(c, 405) })
	return r, nil
}
