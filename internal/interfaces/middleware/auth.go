package middleware

import (
	"crypto/rsa"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"

	"olixops-agent-platform/pkg/logger"
	"olixops-agent-platform/pkg/response"
	"olixops-agent-platform/pkg/userctx"

	"go.uber.org/zap"
)

// Auth JWT 认证中间件。
//
// 规则：
//   - 从请求头 Authorization: Bearer <token> 提取 JWT
//   - 使用配置的 RSA 公钥验签（仅允许 RS256/384/512）
//   - 解析成功后将用户信息（userId/name/jwt/cookie）写入 gin.Context
//   - 任何失败统一返回 HTTP 401 标准错误结构（code/msg 与 HTTP 状态对应）
//
// publicKeyB64 支持两种格式：base64 编码的 DER 公钥，或 PEM 文本。
func Auth(publicKeyB64, issuer, audience string) (gin.HandlerFunc, error) {
	publicKey, err := parsePublicKey(publicKeyB64)
	if err != nil {
		return nil, fmt.Errorf("auth middleware init: %w", err)
	}

	return func(c *gin.Context) {
		tokenString, err := extractBearerToken(c.GetHeader("Authorization"))
		if err != nil {
			logger.Warn(c, "auth failed", zap.Error(err))
			response.AbortWithHTTPStatus(c, 401)
			return
		}

		opts := []jwt.ParserOption{jwt.WithValidMethods([]string{"RS256", "RS384", "RS512"}), jwt.WithExpirationRequired()}
		if issuer != "" {
			opts = append(opts, jwt.WithIssuer(issuer))
		}
		if audience != "" {
			opts = append(opts, jwt.WithAudience(audience))
		}
		claims := jwt.MapClaims{}
		token, err := jwt.ParseWithClaims(tokenString, &claims, func(t *jwt.Token) (any, error) {
			// 限定 RSA 算法，防止算法降级攻击
			if _, ok := t.Method.(*jwt.SigningMethodRSA); !ok {
				return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
			}
			return publicKey, nil
		}, opts...)
		if err != nil || !token.Valid {
			logger.Warn(c, "auth failed: invalid token", zap.Error(err))
			response.AbortWithHTTPStatus(c, 401)
			return
		}

		// 构建用户上下文（对应 Java createUserContext 逻辑）
		user := &userctx.User{
			UserID:    getClaimString(claims, "staffuserid"),
			SessionID: getClaimString(claims, "jti"),
			Name:      getClaimString(claims, "name"),
			Jwt:       tokenString,
			Cookie:    c.GetHeader("Cookie"),
		}
		if strings.TrimSpace(user.UserID) == "" {
			response.AbortWithHTTPStatus(c, 401)
			return
		}
		userctx.Set(c, user)

		c.Next()
	}, nil
}

// extractBearerToken 从 Authorization 头提取 Bearer token
func extractBearerToken(header string) (string, error) {
	if header == "" {
		return "", errors.New("missing Authorization header")
	}
	parts := strings.SplitN(header, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || parts[1] == "" {
		return "", errors.New("invalid Authorization header format, expect: Bearer <token>")
	}
	return parts[1], nil
}

// parsePublicKey 解析配置中的公钥，兼容 base64(DER) 与 PEM 两种格式
func parsePublicKey(key string) (*rsa.PublicKey, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return nil, errors.New("auth.jwt-public-key is empty")
	}

	// PEM 格式（包含 BEGIN 标记）
	if strings.Contains(key, "-----BEGIN") {
		return jwt.ParseRSAPublicKeyFromPEM([]byte(key))
	}

	// base64 编码的 DER 格式（Java 侧常见输出）
	der, err := base64.StdEncoding.DecodeString(key)
	if err != nil {
		return nil, fmt.Errorf("decode base64 public key: %w", err)
	}
	return jwt.ParseRSAPublicKeyFromPEM(wrapPEM(der))
}

// wrapPEM 将 DER 公钥包装为 PEM 块
func wrapPEM(der []byte) []byte {
	encoded := base64.StdEncoding.EncodeToString(der)
	var sb strings.Builder
	sb.WriteString("-----BEGIN PUBLIC KEY-----\n")
	for i := 0; i < len(encoded); i += 64 {
		end := i + 64
		if end > len(encoded) {
			end = len(encoded)
		}
		sb.WriteString(encoded[i:end])
		sb.WriteString("\n")
	}
	sb.WriteString("-----END PUBLIC KEY-----\n")
	return []byte(sb.String())
}

// getClaimString 安全地从 claims 中获取字符串值
func getClaimString(claims jwt.MapClaims, key string) string {
	if v, ok := claims[key].(string); ok {
		return v
	}
	return ""
}
