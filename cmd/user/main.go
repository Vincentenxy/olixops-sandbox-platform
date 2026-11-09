// Provision a local account without public self-registration.
package main

import (
	"context"
	"flag"
	"go.uber.org/zap"
	"olixops-sandbox-platform/internal/infrastructure/config"
	"olixops-sandbox-platform/internal/infrastructure/localauth"
	"olixops-sandbox-platform/internal/infrastructure/store"
	"olixops-sandbox-platform/pkg/logger"
	"os"
	"time"
)

func main() {
	username := flag.String("username", "", "local username")
	name := flag.String("name", "", "display name")
	flag.Parse()
	logger.Init("debug")
	defer logger.Sync()
	c, e := config.Load("./configs")
	if e != nil {
		logger.L().Fatal("load config", zap.Error(e))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	s, e := store.Open(ctx, c.Database.DSN, c.Database.Migrate)
	if e != nil {
		logger.L().Fatal("database", zap.Error(e))
	}
	defer s.DB.Close()
	a, e := localauth.New(ctx, c, s)
	if e != nil {
		logger.L().Fatal("local auth", zap.Error(e))
	}
	if *name == "" {
		*name = *username
	}
	if e = a.CreateUser(ctx, *username, *name, os.Getenv("LOCAL_USER_PASSWORD")); e != nil {
		logger.L().Fatal("provision user", zap.Error(e))
	}
	logger.L().Info("local account provisioned (existing accounts remain unchanged)", zap.String("username", *username))
}
