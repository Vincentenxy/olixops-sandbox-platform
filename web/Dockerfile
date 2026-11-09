FROM node:22-alpine AS builder
WORKDIR /build
COPY web/package*.json ./
RUN npm ci
COPY web/ ./
RUN npm run build
FROM nginxinc/nginx-unprivileged:1.27-alpine
COPY --from=builder /build/dist /usr/share/nginx/html
COPY web/nginx.conf /etc/nginx/conf.d/default.conf
EXPOSE 8080
