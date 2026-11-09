#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
# Reference project requires Go 1.26; allow Go's automatic toolchain selection.
unset GOROOT
export GOTOOLCHAIN=auto
if [ ! -s .local/jwt-public.pem ] || [ ! -s .local/jwt-private.pem ]; then go run ./scripts/dev-auth.go; fi
export AUTH_JWT_PUBLIC_KEY="${AUTH_JWT_PUBLIC_KEY:-$(cat .local/jwt-public.pem)}"
if [ ! -s .local/local-password.txt ]; then
  umask 077
  openssl rand -hex 12 > .local/local-password.txt
fi
export AUTH_LOCAL_BOOTSTRAP_PASSWORD="${AUTH_LOCAL_BOOTSTRAP_PASSWORD:-$(cat .local/local-password.txt)}"
exec go run ./cmd
