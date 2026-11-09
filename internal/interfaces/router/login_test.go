package router_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
	"net/http/httptest"
	"olixops-sandbox-platform/internal/application"
	"olixops-sandbox-platform/internal/infrastructure/config"
	"olixops-sandbox-platform/internal/interfaces/router"
	"olixops-sandbox-platform/pkg/logger"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLocalLoginAndRevocation(t *testing.T) {
	logger.Init("test")
	s := db(t)
	cfg, e := config.Load("../../../configs")
	if e != nil {
		t.Fatal(e)
	}
	cfg.Server.Mode = "test"
	cfg.Auth.Local.Enabled = true
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	keyFile := filepath.Join(t.TempDir(), "private.pem")
	if e = os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}), 0600); e != nil {
		t.Fatal(e)
	}
	cfg.Auth.Local.PrivateKeyFile = keyFile
	cfg.Auth.Local.BootstrapUser = "vincent"
	cfg.Auth.Local.BootstrapPassword = "test-password-123"
	cfg.Auth.Local.TTLSeconds = 3600
	p := &application.Platform{Store: s}
	r, e := router.New(cfg, p)
	if e != nil {
		t.Fatal(e)
	}
	call := func(engine *gin.Engine, route, token string, body any, want int, ip string) json.RawMessage {
		t.Helper()
		var b []byte
		if body != nil {
			b, _ = json.Marshal(body)
		}
		method := "GET"
		if body != nil {
			method = "POST"
		}
		req := httptest.NewRequest(method, "/api/v1/"+route, bytes.NewReader(b))
		req.RemoteAddr = ip + ":12345"
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, req)
		var v struct {
			Code int
			Msg  string
			Data json.RawMessage
		}
		if json.Unmarshal(w.Body.Bytes(), &v) != nil {
			t.Fatal("invalid response")
		}
		if w.Code != want {
			t.Fatalf("%s: got %d want %d", route, w.Code, want)
		}
		if want >= 400 && v.Code != want {
			t.Fatal("invalid error envelope")
		}
		return v.Data
	}
	login := func(pass string, want int) json.RawMessage {
		return call(r, "pub/auth/login", "", map[string]string{"username": "vincent", "password": pass}, want, "127.0.0.1")
	}
	login("incorrect", 401)
	call(r, "pub/auth/login", "", map[string]string{"username": "does-not-exist", "password": "incorrect"}, 401, "127.0.0.1")
	var hash string
	if e = s.DB.QueryRow(context.Background(), "SELECT password_hash FROM local_users WHERE username='vincent'").Scan(&hash); e != nil {
		t.Fatal(e)
	}
	if hash == cfg.Auth.Local.BootstrapPassword || bcrypt.CompareHashAndPassword([]byte(hash), []byte(cfg.Auth.Local.BootstrapPassword)) != nil {
		t.Fatal("password not hashed")
	}
	var result struct {
		AccessToken string `json:"accessToken"`
		TokenType   string `json:"tokenType"`
	}
	_ = json.Unmarshal(login("test-password-123", 200), &result)
	if result.AccessToken == "" || result.TokenType != "Bearer" {
		t.Fatal("missing token")
	}
	parsed, e := jwt.Parse(result.AccessToken, func(t *jwt.Token) (any, error) { return &key.PublicKey, nil }, jwt.WithValidMethods([]string{"RS256"}), jwt.WithIssuer(cfg.Auth.Issuer), jwt.WithAudience(cfg.Auth.Audience))
	if e != nil {
		t.Fatal(e)
	}
	claims := parsed.Claims.(jwt.MapClaims)
	if claims["staffuserid"] != "vincent" || claims["jti"] == "" {
		t.Fatal("wrong identity contract")
	}
	profile := call(r, "user/profile", result.AccessToken, nil, 200, "127.0.0.1")
	if bytes.Contains(profile, []byte("accessToken")) || bytes.Contains(profile, []byte("password")) {
		t.Fatal("profile leaks credentials")
	}
	// Recreate server using the same DB and key. Do not reset the existing password.
	cfg.Auth.Local.BootstrapPassword = "a-different-password"
	r2, e := router.New(cfg, p)
	if e != nil {
		t.Fatal(e)
	}
	call(r2, "user/profile", result.AccessToken, nil, 200, "127.0.0.1")
	call(r2, "auth/logout", result.AccessToken, map[string]any{}, 200, "127.0.0.1")
	call(r, "user/profile", result.AccessToken, nil, 401, "127.0.0.1")
	_ = json.Unmarshal(login("test-password-123", 200), &result)
	_, _ = s.DB.Exec(context.Background(), "UPDATE local_sessions SET expires_at=now()-interval '1 second'")
	call(r, "user/profile", result.AccessToken, nil, 401, "127.0.0.1")
	_ = json.Unmarshal(login("test-password-123", 200), &result)
	_, _ = s.DB.Exec(context.Background(), "UPDATE local_users SET enabled=false")
	call(r, "user/profile", result.AccessToken, nil, 401, "127.0.0.1")
	login("test-password-123", 401)
	// A signature alone no longer grants access in local mode (e.g. the old dev token).
	legacy, _ := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{"staffuserid": "vincent", "iss": cfg.Auth.Issuer, "aud": cfg.Auth.Audience, "exp": time.Now().Add(time.Hour).Unix()}).SignedString(key)
	call(r, "user/profile", legacy, nil, 401, "127.0.0.1")
	for i := 0; i < 10; i++ {
		call(r, "pub/auth/login", "", map[string]string{"username": "none", "password": "incorrect"}, 401, "127.0.0.2")
	}
	call(r, "pub/auth/login", "", map[string]string{"username": "none", "password": "incorrect"}, 429, "127.0.0.2")
}
