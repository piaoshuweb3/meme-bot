# meme-bot 后端镜像（多阶段构建，最终镜像仅含二进制 + 配置）
FROM golang:1.23-alpine AS builder

# 国内构建代理，避免直连 proxy.golang.org 失败
ARG GOPROXY=https://goproxy.cn,direct
ENV GOPROXY=${GOPROXY} GOSUMDB=off CGO_ENABLED=0

WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download || true

COPY . .
RUN go build -trimpath -ldflags "-s -w" -o /out/bot ./cmd/bot \
 && go build -trimpath -ldflags "-s -w" -o /out/migrator ./cmd/migrator \
 && go build -trimpath -ldflags "-s -w" -o /out/worker ./cmd/worker

FROM alpine:3.20

RUN apk add --no-cache ca-certificates tzdata && adduser -D -u 10001 appuser
ENV TZ=UTC

WORKDIR /app
COPY --from=builder /out/bot /app/bot
COPY --from=builder /out/migrator /app/migrator
COPY --from=builder /out/worker /app/worker
COPY migrations /app/migrations
COPY configs /app/configs

USER appuser
EXPOSE 8080
ENTRYPOINT ["/app/bot"]
