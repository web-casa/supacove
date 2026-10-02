# supabackup — single-container production image.
# Multi-stage: build SPA → build Go binary with the SPA embedded → slim runtime.
# Build: docker build --build-arg VERSION=$(git describe --tags --always) -t supabackup .

ARG GO_VERSION=1.26

FROM node:22-bookworm-slim AS frontend
WORKDIR /src
COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci --no-fund --no-audit
COPY frontend/ ./
RUN npm run build

FROM golang:${GO_VERSION}-bookworm AS backend
ARG VERSION=dev
WORKDIR /src
COPY go.mod go.sum ./
COPY backend/ ./backend/
COPY api/ ./api/
# The Go build embeds the SPA; provide it at the embed location.
COPY --from=frontend /src/dist ./backend/internal/web/dist
RUN CGO_ENABLED=0 go build -trimpath \
    -ldflags "-s -w -X main.version=${VERSION}" \
    -o /out/supabackup ./backend/cmd/supabackup

FROM debian:bookworm-slim
# PostgreSQL clients come from PGDG (bookworm's own repo tops out below 18).
RUN apt-get update \
    && apt-get install -y --no-install-recommends curl ca-certificates gnupg \
    && install -d /usr/share/postgres-common/pgdg \
    && curl -fsSL https://www.postgresql.org/media/keys/ACCC4CF8.asc \
        -o /usr/share/postgres-common/pgdg/apt.postgresql.org.asc \
    && echo "deb [signed-by=/usr/share/postgres-common/pgdg/apt.postgresql.org.asc] https://apt.postgresql.org/pub/repos/apt bookworm-pgdg main" \
        > /etc/apt/sources.list.d/pgdg.list \
    && apt-get update \
    && apt-get install -y --no-install-recommends \
       ca-certificates tzdata \
       # P1 ships one client; P2 adds the tested multi-version matrix (14–18).
       postgresql-client-18 \
    && apt-get purge -y curl gnupg \
    && apt-get autoremove -y \
    && rm -rf /var/lib/apt/lists/*

# Non-root runtime user; the data volume must be writable by UID 10001.
# Pre-create the volume mountpoint with the right owner: Docker creates
# anonymous volumes as root:root, which the runtime user could not write.
RUN useradd --uid 10001 --user-group --no-create-home supabackup \
    && mkdir -p /app/data \
    && chown 10001:10001 /app/data
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
