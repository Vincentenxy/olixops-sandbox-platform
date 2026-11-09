package localauth

import (
	"context"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5"
	"golang.org/x/crypto/bcrypt"
	"olixops-agent-platform/internal/infrastructure/config"
	"olixops-agent-platform/internal/infrastructure/store"
	"os"
	"regexp"
	"time"
)

var ErrCredentials = errors.New("invalid credentials")
var usernamePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,63}$`)

type Service struct {
	Store            *store.Store
	Key              *rsa.PrivateKey
	Issuer, Audience string
	TTL              time.Duration
	dummyHash        []byte
}
type LoginResult struct {
	AccessToken string    `json:"accessToken"`
	TokenType   string    `json:"tokenType"`
	ExpiresAt   time.Time `json:"expiresAt"`
}

func New(ctx context.Context, c *config.Config, s *store.Store) (*Service, error) {
	b, e := os.ReadFile(c.Auth.Local.PrivateKeyFile)
	if e != nil {
		return nil, fmt.Errorf("read local signing key: %w", e)
	}
	key, e := jwt.ParseRSAPrivateKeyFromPEM(b)
	if e != nil {
		return nil, e
	}
	if key.N.BitLen() < 2048 {
		return nil, errors.New("local RSA key requires at least 2048 bits")
	}
	if c.Auth.Issuer == "" || c.Auth.Audience == "" || c.Auth.Local.TTLSeconds < 60 || c.Auth.Local.TTLSeconds > 86400 {
		return nil, errors.New("local auth requires issuer, audience and TTL 60..86400")
	}
	pub, e := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if e != nil {
		return nil, e
	}
	c.Auth.JwtPublicKey = string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pub}))
	dummy, e := bcrypt.GenerateFromPassword([]byte(store.ID()), 12)
	if e != nil {
		return nil, e
	}
	a := &Service{s, key, c.Auth.Issuer, c.Auth.Audience, time.Duration(c.Auth.Local.TTLSeconds) * time.Second, dummy}
	if c.Auth.Local.BootstrapUser != "" {
		var exists bool
		e = s.DB.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM local_users WHERE username=$1)", c.Auth.Local.BootstrapUser).Scan(&exists)
		if e != nil {
			return nil, e
		}
		if !exists {
			if c.Auth.Local.BootstrapPassword == "" {
				return nil, errors.New("first start requires AUTH_LOCAL_BOOTSTRAP_PASSWORD")
			}
			if e = a.CreateUser(ctx, c.Auth.Local.BootstrapUser, c.Auth.Local.BootstrapUser, c.Auth.Local.BootstrapPassword); e != nil {
				return nil, e
			}
		}
	}
	return a, nil
}
func (a *Service) CreateUser(ctx context.Context, username, name, password string) error {
	if !usernamePattern.MatchString(username) || name == "" || len(name) > 128 || len(password) < 8 || len(password) > 72 {
		return errors.New("username/name invalid or password length outside 8..72 bytes")
	}
	hash, e := bcrypt.GenerateFromPassword([]byte(password), 12)
	if e != nil {
		return e
	}
	_, e = a.Store.DB.Exec(ctx, `INSERT INTO local_users(user_id,username,name,password_hash,create_by,update_by) VALUES($1,$1,$2,$3,'local-bootstrap','local-bootstrap') ON CONFLICT(username) DO NOTHING`, username, name, string(hash))
	return e
}
func (a *Service) Login(ctx context.Context, username, password string) (LoginResult, error) {
	var id, name, hash string
	var enabled bool
	e := a.Store.DB.QueryRow(ctx, "SELECT user_id,name,password_hash,enabled FROM local_users WHERE username=$1", username).Scan(&id, &name, &hash, &enabled)
	if errors.Is(e, pgx.ErrNoRows) {
		_ = bcrypt.CompareHashAndPassword(a.dummyHash, []byte(password))
		return LoginResult{}, ErrCredentials
	}
	if e != nil {
		return LoginResult{}, e
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) != nil || !enabled {
		return LoginResult{}, ErrCredentials
	}
	now := time.Now()
	expiry := now.Add(a.TTL)
	sid := store.ID()
	raw, e := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{"iss": a.Issuer, "aud": a.Audience, "sub": id, "staffuserid": id, "name": name, "exp": expiry.Unix(), "iat": now.Unix(), "jti": sid}).SignedString(a.Key)
	if e != nil {
		return LoginResult{}, e
	}
	_, e = a.Store.DB.Exec(ctx, `INSERT INTO local_sessions(id,user_id,expires_at,create_by,update_by) VALUES($1,$2,$3,$2,$2)`, sid, id, expiry)
	if e != nil {
		return LoginResult{}, e
	}
	// Remove expired entries opportunistically; errors must not undo a successful login.
	_, _ = a.Store.DB.Exec(ctx, "DELETE FROM local_sessions WHERE expires_at < now()-interval '1 day'")
	return LoginResult{raw, "Bearer", expiry}, nil
}
func (a *Service) Valid(ctx context.Context, sid, user string) (bool, error) {
	var ok bool
	e := a.Store.DB.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM local_sessions s JOIN local_users u ON u.user_id=s.user_id WHERE s.id=$1 AND s.user_id=$2 AND s.expires_at>now() AND u.enabled)`, sid, user).Scan(&ok)
	return ok, e
}
func (a *Service) Logout(ctx context.Context, sid, user string) error {
	_, e := a.Store.DB.Exec(ctx, "DELETE FROM local_sessions WHERE id=$1 AND user_id=$2", sid, user)
	return e
}
