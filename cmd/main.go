package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"olixops-sandbox-platform/internal/application"
	"olixops-sandbox-platform/internal/infrastructure/config"
	"olixops-sandbox-platform/internal/infrastructure/sandbox"
	"olixops-sandbox-platform/internal/infrastructure/storage"
	"olixops-sandbox-platform/internal/infrastructure/store"
	"olixops-sandbox-platform/internal/interfaces/router"
	"olixops-sandbox-platform/pkg/logger"

	"go.uber.org/zap"
)

// @title OlixOps Sandbox Platform API
// @version 0.1.0
// @description 项目授权的 OpenSandbox 管理 API
// @securityDefinitions.apikey BearerAuth
// @in header
// @name Authorization
func main() {
	logger.Init("debug")
	// 加载配置
	cfg, err := config.Load("./configs")
	if err != nil {
		logger.L().Fatal("load config failed", zap.Error(err))
	}

	// 初始化全局日志（全局只允许使用 pkg/logger）
	logger.Init(cfg.Server.Mode)
	defer logger.Sync()

	// 初始化路由
	dbctx, dbcancel := context.WithTimeout(context.Background(), 15*time.Second)
	db, err := store.Open(dbctx, cfg.Database.DSN, cfg.Database.Migrate)
	dbcancel()
	if err != nil {
		logger.L().Fatal("connect database failed", zap.Error(err))
	}
	defer db.DB.Close()
	platform := &application.Platform{Store: db, Admins: cfg.Auth.Admins, Backend: sandbox.New(cfg.Sandbox.BaseURL, cfg.Sandbox.APIKey)}
	if cfg.Workspace.Enabled {
		platform.Storage, err = storage.New(cfg.Workspace.Kubeconfig, cfg.Workspace.Context, cfg.Workspace.ClusterID, cfg.Workspace.Namespace, cfg.Workspace.StorageClass)
		if err != nil {
			logger.L().Fatal("init workspace storage failed", zap.Error(err))
		}
	}
	workerCtx, stopWorker := context.WithCancel(context.Background())
	defer stopWorker()
	go platform.Reconcile(workerCtx)
	r, err := router.New(cfg, platform)
	if err != nil {
		logger.L().Fatal("init router failed", zap.Error(err))
	}

	srv := &http.Server{
		Addr:              fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port),
		Handler:           r,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       90 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}

	// 异步启动 HTTP 服务
	go func() {
		logger.L().Info("server started",
			zap.String("name", cfg.App.Name),
			zap.String("version", cfg.App.Version),
			zap.String("addr", srv.Addr),
		)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.L().Fatal("server listen failed", zap.Error(err))
		}
	}()

	// 优雅退出
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	logger.L().Info("shutting down server...")

	stopWorker()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		logger.L().Fatal("server shutdown failed", zap.Error(err))
	}
	logger.L().Info("server exited")
}
