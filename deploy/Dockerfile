# syntax=docker/dockerfile:1.12
FROM golang:1.25.7-alpine3.23 AS build

ARG TARGETOS=linux
ARG TARGETARCH=amd64
ARG STATE_VERSION=dev

WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags="-s -w -X main.version=${STATE_VERSION}" -o /out/state-server ./cmd/state-server
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags="-s -w -X main.version=${STATE_VERSION}" -o /out/state-relay ./cmd/state-relay
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags="-s -w -X main.version=${STATE_VERSION}" -o /out/statectl ./cmd/statectl

FROM alpine:3.23.3 AS runtime
RUN apk add --no-cache ca-certificates tzdata \
    && addgroup -g 10001 -S state \
    && adduser -u 10001 -S -D -H -G state state
USER 10001:10001

FROM runtime AS state-server
COPY --from=build --chown=10001:10001 /out/state-server /usr/local/bin/state-server
EXPOSE 8090
ENTRYPOINT ["/usr/local/bin/state-server"]
CMD ["serve"]

FROM runtime AS state-relay
COPY --from=build --chown=10001:10001 /out/state-relay /usr/local/bin/state-relay
EXPOSE 8091
ENTRYPOINT ["/usr/local/bin/state-relay"]
CMD ["serve"]

FROM runtime AS statectl
COPY --from=build --chown=10001:10001 /out/statectl /usr/local/bin/statectl
ENTRYPOINT ["/usr/local/bin/statectl"]
