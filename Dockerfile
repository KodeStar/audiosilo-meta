# syntax=docker/dockerfile:1

# AudioSilo Meta - the read-only metadata API server, its baked-in data
# artifact, and the static site, in one image.
#
# Three stages:
#   1. site    - build the Astro static site (site/ -> dist/)
#   2. build   - compile metaserve and bake the current data/ into meta.sqlite
#   3. runtime - a minimal, non-root alpine image running metaserve
#
# At runtime metaserve serves the baked artifact immediately and hot-swaps in
# published data releases. The release workflow can trigger an immediate refresh
# through the signed webhook; --poll remains the recovery path. The baked db is
# the offline fallback; release artifacts carry git-derived added_at dates.

# ---- 1. site -----------------------------------------------------------------
FROM node:24-alpine AS site
WORKDIR /site
# Enable Corepack so the repo's pinned yarn is used.
RUN corepack enable
COPY site/package.json site/yarn.lock ./
RUN yarn install --frozen-lockfile
COPY site/ ./
RUN yarn build
# Astro emits the static site to dist/.

# ---- 2. build ----------------------------------------------------------------
FROM golang:1.25-alpine AS build
WORKDIR /src
ENV CGO_ENABLED=0
# Pure-Go deps (modernc sqlite) so no C toolchain is needed.
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go build -trimpath -ldflags="-s -w" -o /out/metaserve ./cmd/metaserve
# Bake the current data tree into an artifact. added_at falls back to
# sources[].imported_at here; the polled release carries git-derived dates.
RUN go run ./cmd/metabuild -o /out/meta.sqlite

# ---- 3. runtime --------------------------------------------------------------
# Track the current stable Alpine (3.20 went EOL in April 2026).
FROM alpine:3.24 AS runtime
RUN apk add --no-cache ca-certificates \
    && addgroup -S app && adduser -S -G app app \
    && mkdir -p /app /data/cache && chown -R app:app /data
WORKDIR /app
COPY --from=build /out/metaserve /app/metaserve
COPY --from=build /out/meta.sqlite /app/meta.sqlite
COPY --from=site /site/dist /app/site

USER app
EXPOSE 8080
# /data holds the downloaded/hot-swapped release artifacts (poll cache).
VOLUME ["/data"]
ENTRYPOINT ["/app/metaserve", "--db", "/app/meta.sqlite", "--site", "/app/site", "--poll", "--cache", "/data/cache"]
