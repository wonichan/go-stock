# syntax=docker/dockerfile:1
# go-stock Web 服务镜像（自托管）：node 构建前端 → go 以 server 标签静态编译 → debian-slim + chromium
# 构建：docker build -t go-stock-web .
# 运行：见 README-web.md / docker-compose.yml

ARG NODE_VERSION=22
ARG GO_VERSION=1.27.2
ARG DEBIAN_VERSION=trixie

# ---------- 1. 前端 ----------
FROM node:${NODE_VERSION}-${DEBIAN_VERSION}-slim AS frontend
WORKDIR /src/frontend
COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY frontend/ ./
RUN npm run build

# ---------- 2. 后端（-tags server，无 webkit/CGO） ----------
FROM golang:${GO_VERSION}-${DEBIAN_VERSION} AS backend
WORKDIR /src
ENV CGO_ENABLED=0 GOTOOLCHAIN=auto
COPY go.mod go.sum ./
COPY third_party/ ./third_party/
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
COPY --from=frontend /src/frontend/dist ./frontend/dist
ARG VERSION=web-dev
ARG VERSION_COMMIT=
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    go build -tags server -trimpath \
      -ldflags "-s -w -X main.Version=${VERSION} -X main.VersionCommit=${VERSION_COMMIT}" \
      -o /out/go-stock-server .

# ---------- 3. 运行时 ----------
FROM debian:${DEBIAN_VERSION}-slim
# hadolint ignore=DL3008
RUN apt-get update \
 && apt-get install -y --no-install-recommends \
      chromium fonts-noto-cjk ca-certificates tzdata curl \
 && rm -rf /var/lib/apt/lists/* \
 && useradd --system --uid 10001 --home-dir /data --shell /usr/sbin/nologin gostock \
 && mkdir -p /data && chown gostock:gostock /data
# 容器内 Chromium：非 root 且无 user namespace 时沙箱不可用；同时在部署层强制无头
COPY scripts/web/chromium-flags.sh /etc/chromium.d/zz-go-stock
COPY --from=backend /out/go-stock-server /app/go-stock-server

ENV GO_STOCK_ROOT_DIR=/data \
    GO_STOCK_LISTEN=0.0.0.0:8080 \
    GO_STOCK_HTTPS_UPGRADE=on \
    TZ=Asia/Shanghai \
    HOME=/data
USER 10001:10001
WORKDIR /data
VOLUME ["/data"]
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s --start-period=60s --retries=3 \
  CMD ["curl", "-fsS", "-o", "/dev/null", "http://127.0.0.1:8080/healthz"]
ENTRYPOINT ["/app/go-stock-server"]
