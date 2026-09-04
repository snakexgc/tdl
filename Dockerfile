# syntax=docker/dockerfile:1.7
FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS builder

ARG VERSION="dev"
ARG COMMIT="unknown"
ARG COMMIT_DATE="unknown"

WORKDIR /src

COPY . .

ARG TARGETOS
ARG TARGETARCH
ARG TARGETVARIANT
ARG BUILD_MAX_PROCS=2

RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg \
    set -eux; \
    goarm="${TARGETVARIANT#v}"; \
    if [ "$TARGETARCH" != "arm" ]; then goarm=""; fi; \
    GOMAXPROCS=$BUILD_MAX_PROCS CGO_ENABLED=0 \
    GOOS=$TARGETOS GOARCH=$TARGETARCH GOARM=$goarm \
    go build -p=$BUILD_MAX_PROCS -v -trimpath \
    -ldflags "-s -w \
    -X github.com/snakexgc/tdl/pkg/consts.Version=${VERSION}  \
    -X github.com/snakexgc/tdl/pkg/consts.Commit=${COMMIT}  \
    -X github.com/snakexgc/tdl/pkg/consts.CommitDate=${COMMIT_DATE}" \
    -o /out/tdl

FROM alpine:latest

RUN apk add --no-cache ca-certificates tzdata && mkdir -p /app /data

ENV TDL_HOME=/data \
    TDL_DOCKER=true

WORKDIR /data

COPY --from=builder /out/tdl /app/tdl

EXPOSE 22334 22335

ENTRYPOINT ["/app/tdl"]
