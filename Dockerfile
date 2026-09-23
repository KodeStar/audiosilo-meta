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
# loads. A READINESS probe therefore reports the container unready - accurately
# - instead of the container flapping; see the note above the ENTRYPOINT for why
# the image ships no HEALTHCHECK of its own.

# ---- 1. site -----------------------------------------------------------------
# Base images are pinned by DIGEST with the tag kept for readability: a tag is a
# moving pointer, so an unpinned build is not reproducible and a compromised or
# simply retagged upstream lands silently. .github/dependabot.yml owns keeping
# these current - do not hand-edit a digest without the tag it belongs to.
FROM node:24-alpine@sha256:ebfe2f90462722a7a4de65e91990e97fe0d401c70e0e762c5b53302f905ec1c1 AS site
WORKDIR /site
# Enable Corepack so the repo's pinned yarn is used.
RUN corepack enable
COPY site/package.json site/yarn.lock ./
RUN yarn install --frozen-lockfile
COPY site/ ./
# The site imports two files straight out of the Go tree - the genre mapping
# table (site/src/lib/audible-genres.ts) and the OpenAPI spec the /docs/api page
# renders (site/src/pages/docs/api.astro) - so there is exactly one copy of each
# in the repo. Those imports resolve ABOVE /site, so each file has to sit at the
# same relative position here as it does in the repo. Keep these lines in step
# with any further cross-boundary import: site/src/lib/dockerfile-imports.test.ts
# derives the expected COPY set from the site sources and fails when one is
# missing.
COPY internal/importer/audiblegenres.json /internal/importer/audiblegenres.json
COPY internal/serve/openapi.json /internal/serve/openapi.json
RUN yarn build
# Astro emits the static site to dist/.

# ---- 2. build ----------------------------------------------------------------
FROM golang:1.25-alpine@sha256:1ae0735f00daffa3aaf1363a5184c0d2dc55c78e3db4ec70241cdac97bf84b59 AS build
WORKDIR /src
ENV CGO_ENABLED=0
# Pure-Go deps (modernc sqlite) so no C toolchain is needed.
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go build -trimpath -ldflags="-s -w" -o /out/metaserve ./cmd/metaserve

# ---- 3. runtime --------------------------------------------------------------
# Track the current stable Alpine (3.20 went EOL in April 2026).
FROM alpine:3.24@sha256:294b683cb724975bec92580e1e685676bd4b50bda910ddb8c51d4cabeaec77e6 AS runtime
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

# DELIBERATELY NO HEALTHCHECK. /healthz is a READINESS probe - it answers 503
# while the server waits out a GitHub outage, which is a healthy process with no
# data yet - and a Docker healthcheck's "unhealthy" is exactly what autoheal,
# Swarm and compose supervisors RESTART on, turning that patient wait into the
# crash loop the degraded boot exists to avoid. The orchestrator wires /healthz
# as readiness/startup itself; see audiosilo-docs docs-developers/meta/api.md.

ENTRYPOINT ["/app/metaserve", "--site", "/app/site", "--poll", "--cache", "/data/cache"]
