ARG GO_VERSION=1.25
ARG ALPINE_VERSION=3.20
ARG DISTROLESS_TAG=latest

FROM --platform=$BUILDPLATFORM golang:${GO_VERSION}-alpine AS builder

ARG TARGETPLATFORM
ARG TARGETOS
ARG TARGETARCH
ARG TARGETVARIANT

ARG VERSION=dev
ARG BUILD_DATE
ARG VCS_REF
ARG VCS_URL

WORKDIR /src
RUN apk add --no-cache git ca-certificates tzdata && update-ca-certificates

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

COPY . .

ENV CGO_ENABLED=0

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    set -eux; \
    os="${TARGETOS:-$(echo "${TARGETPLATFORM}" | cut -d/ -f1)}"; \
    arch="${TARGETARCH:-$(echo "${TARGETPLATFORM}" | cut -d/ -f2)}"; \
    variant="${TARGETVARIANT:-$(echo "${TARGETPLATFORM}" | cut -d/ -f3)}"; \
    if [ "${arch}" = "arm" ] && [ -n "${variant}" ]; then export GOARM="${variant#v}"; fi; \
    export GOOS="${os}" GOARCH="${arch}"; \
    echo ">> building for GOOS=${GOOS} GOARCH=${GOARCH} GOARM=${GOARM:-} (TARGETPLATFORM=${TARGETPLATFORM:-unknown})"; \
    PKG_VER=$(go list -m -f '{{.Path}}')/version; \
    go build -mod=readonly -trimpath \
      -ldflags="-s -w -buildid= \
                -X '${PKG_VER}.Version=${VERSION}' \
                -X '${PKG_VER}.BuildDate=${BUILD_DATE}' \
                -X '${PKG_VER}.GitCommit=${VCS_REF}' \
                -X '${PKG_VER}.GitURL=${VCS_URL}'" \
      -o /out/moonraker2mqtt ./cmd/main.go

RUN mkdir -p /out/logs \
 && ln -sf /dev/stdout /out/logs/moonraker2mqtt.log

FROM golang:${GO_VERSION}-alpine AS development
RUN apk add --no-cache git bash curl make tzdata ca-certificates && update-ca-certificates
RUN go install github.com/air-verse/air@latest \
 && go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.4.0
WORKDIR /workspace
ENV GOTOOLCHAIN=auto \
    CGO_ENABLED=0 \
    GOCACHE="/workspace/tmp/.cache" \
    GOLANGCI_LINT_CACHE="/workspace/tmp/.cache" \
    GOMODCACHE="/workspace/tmp/.cache/mod" \
    GOCACHE="/workspace/tmp/.cache/go-build" \
    XDG_CACHE_HOME="/workspace/tmp/.cache" \
    GOLANGCI_LINT_CACHE="/workspace/tmp/.cache/golangci-lint" \
    PATH="/go/bin:${PATH}"

CMD ["air", "-c", ".air.toml"]

FROM gcr.io/distroless/static-debian13:${DISTROLESS_TAG} AS distroless
WORKDIR /app

ARG VERSION=dev
ARG BUILD_DATE
ARG VCS_REF
ARG VCS_URL

LABEL maintainer="Alain CAJUSTE <cajuste.alain@gmail.com>" \
      org.opencontainers.image.title="moonraker2mqtt" \
      org.opencontainers.image.description="Bridge between Moonraker and MQTT" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.created="${BUILD_DATE}" \
      org.opencontainers.image.revision="${VCS_REF}" \
      org.opencontainers.image.source="${VCS_URL}" \
      org.opencontainers.image.vendor="AC-CodeProd" \
      org.opencontainers.image.licenses="GPL-3.0" \
      org.opencontainers.image.base.name="gcr.io/distroless/static-debian13"

USER 65532:65532

COPY --from=builder --chown=65532:65532 /out/moonraker2mqtt /app/moonraker2mqtt
COPY --from=builder --chown=65532:65532 /src/config.yaml /app/config.yaml
COPY --from=builder --chown=65532:65532 /out/logs /app/logs

ENV LOG_LEVEL=info \
    ENVIRONMENT=production

ENTRYPOINT ["/app/moonraker2mqtt"]
CMD ["-config", "/app/config.yaml"]

FROM alpine:${ALPINE_VERSION} AS alpine
RUN addgroup -S app && adduser -S -G app app \
    && apk add --no-cache ca-certificates tzdata bash curl \
    && update-ca-certificates
WORKDIR /app
RUN mkdir -p /app/logs && chown -R app:app /app
RUN ln -sf /dev/stdout /app/logs/moonraker2mqtt.log
USER app
COPY --from=builder --chown=app:app /out/moonraker2mqtt /app/moonraker2mqtt
COPY --from=builder --chown=app:app /src/config.yaml /app/config.yaml

ENV LOG_LEVEL=info \
    ENVIRONMENT=production

ENTRYPOINT ["/app/moonraker2mqtt"]
CMD ["-config", "/app/config.yaml"]
