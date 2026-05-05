# syntax=docker/dockerfile:1.7
FROM --platform=$BUILDPLATFORM golang:1.25-alpine AS builder

ARG VERSION="dev"
ARG COMMIT="unknown"
ARG COMMIT_DATE="unknown"

WORKDIR /src

COPY . .

ARG TARGETOS
ARG TARGETARCH
ARG TARGETVARIANT

RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg \
    set -eux; \
    goarm="${TARGETVARIANT#v}"; \
    if [ "$TARGETARCH" != "arm" ]; then goarm=""; fi; \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH GOARM=$goarm \
    go build -trimpath \
    -ldflags "-s -w \
    -X github.com/iyear/tdl/pkg/consts.Version=${VERSION}  \
    -X github.com/iyear/tdl/pkg/consts.Commit=${COMMIT}  \
    -X github.com/iyear/tdl/pkg/consts.CommitDate=${COMMIT_DATE}" \
    -o /out/tdl

FROM alpine:latest

RUN apk add --no-cache ca-certificates tzdata

WORKDIR /app

COPY --from=builder /out/tdl /app/tdl

EXPOSE 22334 22335

ENTRYPOINT ["/app/tdl"]
