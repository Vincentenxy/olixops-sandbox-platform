package config

import (
	"fmt"
	"github.com/spf13/viper"
	"net/url"
	"strings"
)

type Config struct {
	Server struct {
		Port int
		Mode string
		Host string
	} `mapstructure:"server"`
	App struct {
		Name    string
		Version string
	} `mapstructure:"app"`
	Auth struct {
		JwtPublicKey string `mapstructure:"jwt-public-key"`
		Issuer       string
		Audience     string
		Admins       []string
		Local        struct {
			Enabled           bool
			PrivateKeyFile    string `mapstructure:"private-key-file"`
			TTLSeconds        int    `mapstructure:"ttl-seconds"`
			BootstrapUser     string `mapstructure:"bootstrap-user"`
			BootstrapPassword string `mapstructure:"bootstrap-password"`
		} `mapstructure:"local"`
	} `mapstructure:"auth"`
	Database struct {
		DSN     string
		Migrate bool
	} `mapstructure:"database"`
	Workspace struct {
		Enabled      bool
		Kubeconfig   string
		Context      string
		ClusterID    string `mapstructure:"cluster-id"`
		Namespace    string
		StorageClass string `mapstructure:"storage-class"`
		SizeBytes    int64  `mapstructure:"size-bytes"`
		ProjectQuota int    `mapstructure:"project-quota"`
		ProjectBytes int64  `mapstructure:"project-bytes"`
	} `mapstructure:"workspace"`
	Sandbox struct {
		BaseURL      string `mapstructure:"base-url"`
		APIKey       string `mapstructure:"api-key"`
		Images       []string
		MaxTTL       int `mapstructure:"max-ttl"`
		DefaultQuota int `mapstructure:"default-quota"`
	} `mapstructure:"sandbox"`
}

func Load(path string) (*Config, error) {
	v := viper.New()
	v.SetConfigName("config")
	v.SetConfigType("yaml")
	v.AddConfigPath(path)
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_", "-", "_"))
	v.AutomaticEnv()
	for _, k := range []string{"auth.jwt-public-key", "auth.issuer", "auth.audience", "database.dsn", "sandbox.api-key", "sandbox.base-url", "server.host", "workspace.kubeconfig", "workspace.context", "workspace.enabled", "auth.local.bootstrap-password", "auth.local.private-key-file"} {
		_ = v.BindEnv(k)
	}
	if err := v.ReadInConfig(); err != nil {
		return nil, err
	}
	var c Config
	if err := v.Unmarshal(&c); err != nil {
		return nil, err
	}
	u, err := url.Parse(c.Sandbox.BaseURL)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("sandbox.base-url must be an http(s) origin with optional prefix")
	}
	c.Sandbox.BaseURL = strings.TrimRight(c.Sandbox.BaseURL, "/")
	if c.Database.DSN == "" || len(c.Sandbox.Images) == 0 || c.Sandbox.MaxTTL < 60 || c.Sandbox.DefaultQuota < 1 {
		return nil, fmt.Errorf("database, images, max-ttl and default-quota must be configured")
	}
	if c.Workspace.Enabled && (c.Workspace.ClusterID == "" || c.Workspace.Namespace == "" || c.Workspace.StorageClass == "" || c.Workspace.SizeBytes < 1 || c.Workspace.ProjectQuota < 1 || c.Workspace.ProjectBytes < c.Workspace.SizeBytes) {
		return nil, fmt.Errorf("workspace storage target and quotas must be configured")
	}
	return &c, nil
}
