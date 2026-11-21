# 多阶段构建：builder 用完整 Go 镜像，最终阶段用不含 shell 的 distroless
# ——jackc/pgx/v5 是纯 Go 实现，CGO_ENABLED=0 编译出的二进制不需要任何
# 系统库。
#
# 只用 docker/podman 都支持的标准 Dockerfile 语法（多阶段 FROM ... AS、
# 标准 RUN/COPY），不用 BuildKit 专属的 heredoc（`RUN <<EOF`）之类的
# 扩展写法——`docker build` 和 `podman build`（buildah 后端）都能直接
# 构建这份文件，不需要额外配置构建后端。podman 默认产出 OCI 格式镜像，会忽略下面的
# HEALTHCHECK，需要时加 `--format docker`（见 docker-compose.yml 末尾的 Podman 说明）。
#
# 后台前端（web/admin）在 Node 阶段构建，产物拷进 api 的 Go 构建阶段，经 go:embed 打进二进制；
# worker 不需要前端，构建 worker 镜像时不会执行 Node 阶段。Node、pnpm 版本由
# scripts/check-versions.sh 与 web/admin/package.json、CI 对齐。

# ---- 后台前端 ----
FROM node:24-alpine AS web

RUN npm install -g pnpm@12.10.0

WORKDIR /web

# 依赖层单独缓存，package.json / lockfile 没变时不用重新安装
COPY web/admin/package.json web/admin/pnpm-lock.yaml web/admin/pnpm-workspace.yaml ./
RUN pnpm install --frozen-lockfile

COPY web/admin/ ./
RUN pnpm build

# ---- Go 构建 ----
FROM golang:1.27-alpine AS builder

WORKDIR /src

# 依赖层单独缓存，源码没变时不用重新下载模块
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# 日志的 version 字段：docker build --build-arg VERSION=v1.2.3。这里的构建不带 VCS 信息，不传时是 dev。
ARG VERSION=""
ENV LDFLAGS="-X app.local/internal/platform/buildinfo.version=${VERSION}"

# CGO_ENABLED=0 产出静态二进制，配合 distroless/static 使用
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags "$LDFLAGS" -o /out/worker ./cmd/worker

FROM builder AS api-builder

COPY --from=web /web/dist/ web/admin/dist/
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags "$LDFLAGS" -o /out/api ./cmd/api

# ---- api 镜像 ----
FROM gcr.io/distroless/static-debian13:nonroot AS api

# distroless 的 nonroot 变体默认就是非 root 用户（uid/gid 65532）——
# 容器不用 root 用户跑是安全基线要求，不是可选加固。这里显式
# 写出 USER 指令，让这条要求在 Dockerfile 里可见，不依赖"恰好选对了
# nonroot 标签"这个隐式前提。
USER nonroot:nonroot

COPY --from=api-builder /out/api /app/api

# 没有 shell/curl 可用，健康检查复用二进制自己的 `healthcheck` 子命令
# （见 internal/platform/healthcheck）。注意：这只让 `docker ps`/`podman ps` 能看到状态、
# 并驱动 docker-compose 的 depends_on condition，纯 docker/podman
# （非 Swarm/K8s）判定 unhealthy 后不会自动重启容器。
HEALTHCHECK --interval=10s --timeout=3s --start-period=5s --retries=3 \
    CMD ["/app/api", "healthcheck"]

ENTRYPOINT ["/app/api"]

# ---- worker 镜像 ----
FROM gcr.io/distroless/static-debian13:nonroot AS worker

USER nonroot:nonroot

COPY --from=builder /out/worker /app/worker

HEALTHCHECK --interval=10s --timeout=3s --start-period=5s --retries=3 \
    CMD ["/app/worker", "healthcheck"]

ENTRYPOINT ["/app/worker"]
