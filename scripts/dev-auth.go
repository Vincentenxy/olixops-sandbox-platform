// Generate local RSA signing keys. Accounts and tokens are managed by the login service.
package main

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"github.com/golang-jwt/jwt/v5"
	"os"
	"path/filepath"
)

func main() {
	dir := ".local"
	must(os.MkdirAll(dir, 0700))
	keyPath := filepath.Join(dir, "jwt-private.pem")
	var key *rsa.PrivateKey
	if b, e := os.ReadFile(keyPath); e == nil {
		key, e = jwt.ParseRSAPrivateKeyFromPEM(b)
		must(e)
	} else {
		var e error
		key, e = rsa.GenerateKey(rand.Reader, 2048)
		must(e)
		must(os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}), 0600))
	}
	pub, e := x509.MarshalPKIXPublicKey(&key.PublicKey)
	must(e)
	must(os.WriteFile(filepath.Join(dir, "jwt-public.pem"), pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pub}), 0600))
}
func must(e error) {
	if e != nil {
		panic(e)
	}
}
