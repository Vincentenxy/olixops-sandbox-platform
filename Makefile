.PHONY: run test build web swagger
run:
	./scripts/dev.sh
test:
	env -u GOROOT go test -race ./...
build:
	env -u GOROOT go build -o bin/server ./cmd
web:
	npm run build --prefix web
swagger:
	env -u GOROOT go run github.com/swaggo/swag/cmd/swag@v1.16.6 init -g cmd/main.go -o docs --outputTypes json,yaml
