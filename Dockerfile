# syntax=docker/dockerfile:1

# AudioSilo Meta - the read-only metadata API server and the static site, in one
# image. NO data is baked in: the catalogue comes from the published data
# release at boot.
#
# Three stages:
#   1. site    - build the Astro static site (site/ -> dist/)
#   2. build   - compile metaserve
#   3. runtime - a minimal, non-root alpine image running metaserve
#
# Why the data is not in the image: the artifact is a data release, published on
# its own cadence, and it grows with the catalogue (hundreds of MB once the
# libex seed lands). Baking it made every site tweak re-validate and re-compile
# the whole catalogue and ship a data-sized image, and a container that outlived
# its build served stale bytes until the first poll. Now the image is code +
# site only, a UI change rebuilds neither, and metaserve fetches the newest
# release before it serves the first request (poll-only boot: New() does the
# first refresh synchronously).
#
# Boot failure mode - deliberate: if GitHub is unreachable at startup the
# process does NOT exit (that would be a container crash loop). It comes up
# degraded, logs the reason, serves the static site, answers /healthz with
# `{"status":"starting"}` + 503 and every API route with 503, and retries -
# first after 30s, then backing off to the poll interval - until a release
# loads. A health check therefore reports the container unready - accurately -
# instead of the container flapping.

# ---- 1. site -----------------------------------------------------------------
FROM node:24-alpine AS site
WORKDIR /site
# Enable Corepack so the repo's pinned yarn is used.
RUN corepack enable
COPY site/package.json site/yarn.lock ./
RUN yarn install --frozen-lockfile
COPY site/ ./
# The site imports the genre mapping table straight out of the Go importer
# (site/src/lib/audible-genres.ts -> internal/importer/audiblegenres.json) so
# there is exactly one copy of it in the repo. That import resolves ABOVE /site,
# so the file has to sit at the same relative position here as it does in the
# repo. Keep this line in step with any further cross-boundary import.
COPY internal/importer/audiblegenres.json /internal/importer/audiblegenres.json
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

# ---- 3. runtime --------------------------------------------------------------
# Track the current stable Alpine (3.20 went EOL in April 2026).
FROM alpine:3.24 AS runtime
RUN apk add --no-cache ca-certificates \
    && addgroup -S app && adduser -S -G app app \
    && mkdir -p /app /data/cache && chown -R app:app /data
WORKDIR /app
COPY --from=build /out/metaserve /app/metaserve
COPY --from=site /site/dist /app/site

USER app
EXPOSE 8080
# /data holds the downloaded/hot-swapped release artifacts. It is a disposable
# cache: a boot always fetches the newest release (the first refresh is always
# full - there is no loaded artifact to patch against), so persisting it does
# NOT shorten startup. What it buys is the patch base for the refreshes that
# follow, so later updates transfer a delta instead of the whole artifact.
VOLUME ["/data"]
ENTRYPOINT ["/app/metaserve", "--site", "/app/site", "--poll", "--cache", "/data/cache"]
