# enowx-rag server image: HTTP API + dashboard + MCP at /mcp.
#
#   docker build -t enowx-rag ./mcp-server
#   docker build --build-arg WEB=prebuilt -t enowx-rag ./mcp-server   # skip npm (small VPS)
#
# WEB=build (default) builds the dashboard with npm; WEB=prebuilt embeds the
# committed web/dist as is (what releases do) — faster and needs far less RAM.
ARG WEB=build

# Stage 1a: build the React dashboard
FROM node:24-alpine AS web-build
WORKDIR /web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ .
RUN npm run build

# Stage 1b: or take the committed build
FROM alpine:3.21 AS web-prebuilt
COPY web/dist /web/dist

FROM web-${WEB} AS web

# Stage 2: build the Go binary (with the dashboard embedded)
FROM golang:1.26-alpine AS go-builder
WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /web/dist ./web/dist
ARG VERSION=docker
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o enowx-rag ./cmd/mcp-server

# Stage 3: minimal runtime, non-root. Everything enowx-rag keeps (config.yaml,
# metrics, activity, uploads, the SQLite store if used) lives in /data/.enowx-rag.
FROM alpine:3.21
RUN apk add --no-cache ca-certificates tzdata \
	&& adduser -D -u 10001 -h /data enowx \
	&& mkdir -p /data/.enowx-rag && chown -R enowx:enowx /data
WORKDIR /app
COPY --from=go-builder /build/enowx-rag /usr/local/bin/enowx-rag
USER enowx
ENV HOME=/data
VOLUME /data
EXPOSE 7777
HEALTHCHECK --interval=30s --timeout=5s --start-period=90s --retries=3 \
	CMD wget -qO- http://127.0.0.1:7777/api/health >/dev/null || exit 1
ENTRYPOINT ["enowx-rag"]
CMD ["--serve", "--addr", ":7777"]
