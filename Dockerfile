# supabackup — single-container production image.
# Multi-stage: build SPA → build Go binary with the SPA embedded → slim runtime.

FROM node:22-bookworm-slim AS frontend
WORKDIR /src
COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci --no-fund --no-audit
COPY frontend/ ./
RUN npm run build

FROM golang:1.24-bookworm AS backend
WORKDIR /src
COPY go.mod go.sum ./
COPY backend/ ./backend/
COPY api/ ./api/
# The Go build embeds the SPA; provide it at the embed location.
COPY --from=frontend /src/dist ./backend/internal/web/dist
RUN CGO_ENABLED=0 go build -trimpath \
    -ldflags "-s -w -X main.version=$(git describe --tags --always 2>/dev/null || echo dev)" \
    -o /out/supabackup ./backend/cmd/supabackup

FROM debian:bookworm-slim
RUN apt-get update \
    && apt-get install -y --no-install-recommends \
       ca-certificates tzdata \
       # P1 ships one client; P2 adds the tested multi-version matrix (14–18).
       postgresql-client-18 \
    && rm -rf /var/lib/apt/lists/*

# Non-root runtime user; data volume must be writable by UID 10001.
RUN useradd --uid 10001 --user-group --no-create-home supabackup
WORKDIR /app
COPY --from=backend /out/supabackup /app/supabackup

ENV SB_DATA_DIR=/app/data \
    SB_ADDR=:8080
VOLUME ["/app/data"]
EXPOSE 8080
USER 10001:10001

HEALTHCHECK --interval=30s --timeout=3s --start-period=5s \
    CMD ["/app/supabackup", "healthcheck"]

ENTRYPOINT ["/app/supabackup"]
CMD ["serve"]
