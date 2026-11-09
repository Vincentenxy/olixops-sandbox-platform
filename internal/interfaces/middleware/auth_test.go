package middleware

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"olixops-agent-platform/pkg/userctx"
)

func TestAuthKeepsCookieOnlyInInternalUserContext(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	public, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	auth, err := Auth(string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: public})), "test", "platform")
	if err != nil {
		t.Fatal(err)
	}
	r := gin.New()
	r.GET("/", auth, func(c *gin.Context) {
		u := userctx.FromContext(c)
		if u == nil || u.Cookie != "opaque=session-value" {
			t.Error("request cookie missing from authenticated context")
		}
		c.JSON(200, u)
	})
	token, err := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{"staffuserid": "alice", "iss": "test", "aud": "platform", "exp": time.Now().Add(time.Hour).Unix()}).SignedString(key)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Cookie", "opaque=session-value")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	var data map[string]any
	if err = json.Unmarshal(rec.Body.Bytes(), &data); err != nil {
		t.Fatal(err)
	}
	if rec.Code != 200 || len(data) != 2 || data["userId"] != "alice" {
		t.Fatalf("credentials leaked in user JSON: %s", rec.Body)
	}
}
