FROM golang:1.26-alpine AS builder
WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /app/server ./cmd
FROM alpine:3.21
RUN apk add --no-cache ca-certificates tzdata
WORKDIR /app
COPY --from=builder /app/server .
COPY --from=builder /build/configs ./configs
ENV SERVER_HOST=0.0.0.0
USER 10001:10001
EXPOSE 18090
ENTRYPOINT ["/app/server"]
