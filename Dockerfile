ARG GO_VERSION=1.25
ARG ALPINE_VERSION=3.20
ARG DISTROLESS_TAG=latest

FROM --platform=$BUILDPLATFORM golang:${GO_VERSION}-alpine AS builder

ARG TARGETOS
ARG TARGETARCH
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
    GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -mod=readonly -trimpath \
      -ldflags="-s -w -buildid= \
                -X 'main.Version=${VERSION}' \
                -X 'main.BuildDate=${BUILD_DATE}' \
                -X 'main.GitCommit=${VCS_REF}' \
                -X 'main.GitURL=${VCS_URL}'" \
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
USER app
RUN mkdir -p /app /app/logs
COPY --from=builder --chown=app:app /out/moonraker2mqtt /app/moonraker2mqtt
COPY --from=builder --chown=app:app /src/config.yaml /app/config.yaml

ENV LOG_LEVEL=info \
    ENVIRONMENT=production

ENTRYPOINT ["/app/moonraker2mqtt"]
CMD ["-config", "/app/config.yaml"]
