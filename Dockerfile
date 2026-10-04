# supabackup — single-container production image.
# Multi-stage: build SPA → build Go binary with the SPA embedded → slim runtime.
# Build (release = the `runtime` stage; ALWAYS pass --target when scripting):
#   docker build --target runtime -t supabackup .
# The file's last stage is runtime-spike (PG server binaries for ADR-004
# experiments); compose and CI pin target: runtime and it must never ship.

ARG GO_VERSION=1.26

FROM node:22-bookworm-slim AS frontend
WORKDIR /src
COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci --no-fund --no-audit
COPY frontend/ ./
RUN npm run build

FROM golang:${GO_VERSION}-bookworm AS backend
ARG VERSION=dev
ARG COMMIT=unknown
ARG BUILD_DATE=unknown
WORKDIR /src
COPY go.mod go.sum ./
COPY backend/ ./backend/
COPY api/ ./api/
# The Go build embeds the SPA; provide it at the embed location.
COPY --from=frontend /src/dist ./backend/internal/web/dist
RUN CGO_ENABLED=0 go build -trimpath \
    -ldflags "-s -w -X main.version=${VERSION} -X main.commit=${COMMIT} -X main.buildDate=${BUILD_DATE}" \
    -o /out/supabackup ./backend/cmd/supabackup


FROM debian:bookworm-slim AS runtime
# PostgreSQL clients from PGDG (bookworm's own repo tops out below 18), plus
# the PG 18 SERVER for the embedded restore verifier (ADR-004). The verifier
# stays DISABLED unless the administrator sets SB_VERIFY_ENABLED=1 and
# supplies the age identity (phase-5 review P1-04: shipping the binaries is
# what makes the documented feature actually usable in the release image).
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
       # Multi-version client matrix per dev-plan §0: the kernel picks the
       # client matching the SERVER major (same major preferred; never older
       # than the server).
       postgresql-client-14 postgresql-client-15 postgresql-client-16 \
       postgresql-client-17 postgresql-client-18 \
       postgresql-18 \
    && apt-get purge -y curl gnupg \
    && apt-get autoremove -y \
    && rm -rf /var/lib/apt/lists/*

# Non-root runtime user; the data volume must be writable by UID 10001.
# Pre-create the volume mountpoint with the right owner: Docker creates
# anonymous volumes as root:root, which the runtime user could not write.
RUN useradd --uid 10001 --user-group --no-create-home supabackup \
    && install -d -o 10001 -g 10001 -m 0700 /app/data
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

# Runtime-shape verification target for Spike 2 (ADR-004): the release runtime
# plus PostgreSQL server binaries, same non-root UID 10001, for the
# embedded-verifier feasibility experiment. compose/CI pin target: runtime;
# this last stage must never be shipped by default.

# Runtime-shape verification target for Spike 2 (ADR-004): the release runtime
# plus PostgreSQL server binaries, same non-root UID 10001, for the
# embedded-verifier feasibility experiment. Not used by releases.
FROM runtime AS runtime-spike
USER root
RUN apt-get update \
    && apt-get install -y --no-install-recommends postgresql-18 postgresql-client-18 procps \
    && rm -rf /var/lib/apt/lists/*
USER 10001:10001
