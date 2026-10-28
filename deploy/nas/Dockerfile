# syntax=docker/dockerfile:1
ARG NODE_VERSION=22.23.4-alpine3.24
ARG GO_VERSION=1.27.3-alpine3.24

FROM --platform=$BUILDPLATFORM node:${NODE_VERSION} AS web
WORKDIR /src/web
# Lock the package manager as well as the dependencies. pnpm-workspace.yaml
# permits only the exact esbuild install script needed by this lockfile.
RUN npm install --global pnpm@11.25.0
COPY web/package.json web/pnpm-lock.yaml web/pnpm-workspace.yaml ./
RUN pnpm install --frozen-lockfile
COPY web/ ./
RUN pnpm build

FROM --platform=$BUILDPLATFORM golang:${GO_VERSION} AS build
ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev
ARG REVISION=unknown
ARG CREATED=unknown
WORKDIR /src/server
RUN apk add --no-cache ca-certificates
COPY server/go.mod server/go.sum ./
RUN go mod download && go mod verify
COPY server/ ./
COPY --from=web /src/server/internal/webui/dist ./internal/webui/dist
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -tags webassets \
    -trimpath -buildvcs=false \
    -ldflags="-s -w -X main.version=${VERSION} -X main.revision=${REVISION} -X main.created=${CREATED}" \
    -o /out/relay ./cmd/relay

FROM scratch
ARG VERSION=dev
ARG REVISION=unknown
ARG CREATED=unknown
LABEL org.opencontainers.image.title="Relay Service" \
      org.opencontainers.image.description="NAS service monitoring development preview" \
      org.opencontainers.image.source="https://github.com/DavisDing/Relay-Service" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.revision="${REVISION}" \
      org.opencontainers.image.created="${CREATED}"
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build --chown=65532:65532 /out/relay /relay
ENV RELAY_DEPLOYMENT_FILE=/relay-secrets/current/deployment.env \
    RELAY_INITIAL_PASSWORD_FILE=/relay-secrets/initial/password
HEALTHCHECK --interval=30s --timeout=5s --start-period=20s --retries=3 \
  CMD ["/relay", "healthcheck"]
EXPOSE 8787
USER 65532:65532
ENTRYPOINT ["/relay"]
