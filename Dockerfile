# syntax=docker/dockerfile:1
#
# bekup container image: one image, several subcommands (D-025).
#   docker run ... bekup serve     # web/API backend
#   docker run ... bekup runner    # the only component with Docker access (D-003)
#
# Stages:
#   web      Vite build of web/ (placeholder page when web/ is absent or skipped)
#   build    static Go binary with the web build embedded
#   runtime  distroless static, nonroot, no shell (default target)
#   dev      Go toolchain + air hot reload, used only by docker-compose.dev.yml
#
# Every base image is pinned by tag AND multi-arch index digest. Renovate
# (docker:pinDigests, renovate.json) keeps the digests current; never drop the
# digest when bumping a tag (D-061).
#
# Build:  docker build --build-arg VERSION=$(cat VERSION) -t bekup:dev .
#         (or `just docker`). Multi-arch: docker buildx build --platform
#         linux/amd64,linux/arm64 ... (Go cross-compiles; no emulation needed).

ARG NODE_IMAGE=node:22-alpine@sha256:fe4f6d74e20370acb04ad889077d14d0f64a9c8ea6a42bc60cddf1e844699914
ARG GO_IMAGE=golang:1.27-alpine@sha256:b8c9b82affbbb82a020bbcf998dad2ba9516944ac7e883524642a481a9505e9c
ARG RUNTIME_IMAGE=gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab

# ---------------------------------------------------------------------------
# Stage 1: web build
# ---------------------------------------------------------------------------
# websrc: the web/ tree, or nothing when web/ does not exist yet. `we[b]` is a
# glob, and COPY only fails when *no* source matches; VERSION always exists.
# (A plain web/... path would fail the build while web/ is missing.)
FROM scratch AS websrc
COPY VERSION we[b] /websrc/

FROM --platform=$BUILDPLATFORM ${NODE_IMAGE} AS web

# auto: build web/ when web/package.json exists, otherwise emit a placeholder.
# skip: always emit the placeholder (Go-only builds, CI jobs without node).
ARG WEB_BUILD=auto

ENV CI=true \
    COREPACK_ENABLE_DOWNLOAD_PROMPT=0 \
    PNPM_HOME=/pnpm \
    NPM_CONFIG_UPDATE_NOTIFIER=false

WORKDIR /src/web

# Manifests first so the dependency layer stays cached while only sources
# change. The globs make every file optional (VERSION always matches).
COPY --from=websrc /websrc/VERSION /websrc/package.json* /websrc/pnpm-lock.yaml* /websrc/pnpm-workspace.yaml* /websrc/.npmrc* ./
# pnpm's patchedDependencies (pnpm-workspace.yaml) must exist before install.
# A matched directory copies its contents; VERSION keeps the glob optional.
COPY --from=websrc /websrc/VERSION /websrc/patche[s] ./patches/

# pnpm comes from corepack at the exact version in package.json's
# "packageManager" field. --frozen-lockfile refuses any lockfile drift, and
# pnpm-workspace.yaml (strictDepBuilds/allowBuilds) blocks install scripts.
RUN --mount=type=cache,id=bekup-pnpm-store,target=/pnpm/store \
    set -eu; \
    if [ "${WEB_BUILD}" != "skip" ] && [ -f package.json ]; then \
        corepack enable; \
        pnpm install --frozen-lockfile --store-dir /pnpm/store; \
    fi

COPY --from=websrc /websrc/ ./

RUN set -eu; \
    if [ "${WEB_BUILD}" != "skip" ] && [ -f package.json ]; then \
        pnpm build; \
    else \
        echo "web/ not present or WEB_BUILD=skip: emitting placeholder UI"; \
        rm -rf dist; mkdir -p dist; \
        printf '%s\n' '<!doctype html>' \
          '<html lang="en"><head><meta charset="utf-8"><title>bekup</title></head>' \
          '<body><p>bekup: the web UI was not included in this build.</p></body></html>' \
          > dist/index.html; \
    fi; \
    test -f dist/index.html

# ---------------------------------------------------------------------------
# Stage 2: Go build
# ---------------------------------------------------------------------------
FROM --platform=$BUILDPLATFORM ${GO_IMAGE} AS build

ARG TARGETOS
ARG TARGETARCH
# Overrides the VERSION file (e.g. `just docker` passes it explicitly).
ARG VERSION=""
# Package path of the ldflags version variable.
ARG VERSION_VAR=main.version

# GOTOOLCHAIN=local: never download a different toolchain during the build.
# GOFLAGS=-mod=readonly: go.mod/go.sum must already be complete.
# GOWORK=off: a local go.work never changes the module graph of the image.
ENV CGO_ENABLED=0 \
    GOTOOLCHAIN=local \
    GOFLAGS=-mod=readonly \
    GOWORK=off

WORKDIR /src

COPY go.mod go.sum* ./
# Local replace targets of go.mod (D-208: the inert clipboard module).
COPY third_party/ ./third_party/
RUN --mount=type=cache,id=bekup-gomod,target=/go/pkg/mod \
    go mod download && go mod verify

COPY . .
# The embedded UI (web/embed.go uses go:embed on web/dist).
COPY --from=web /src/web/dist ./web/dist

# The version comes from --build-arg VERSION or the VERSION file and is
# checked against strict SemVer before it reaches -ldflags. The empty
# directories become nonroot-owned mount points: named volumes mounted there
# inherit this ownership on first use, so the nonroot user can write to them.
RUN --mount=type=cache,id=bekup-gomod,target=/go/pkg/mod \
    --mount=type=cache,id=bekup-gobuild,target=/root/.cache/go-build \
    set -eu; \
    v="${VERSION:-$(cat VERSION)}"; \
    v="${v#v}"; \
    echo "$v" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?(\+[0-9A-Za-z.-]+)?$' \
      || { echo "invalid VERSION: $v" >&2; exit 1; }; \
    GOOS="${TARGETOS:-linux}" GOARCH="${TARGETARCH:-amd64}" \
      go build -trimpath -buildvcs=false \
        -ldflags "-s -w -buildid= -X ${VERSION_VAR}=${v}" \
        -o /out/bekup ./cmd/bekup; \
    mkdir -p /out/varlib/backups /out/varlib/scratch

# ---------------------------------------------------------------------------
# Dev stage: hot reload with air (docker-compose.dev.yml only, never shipped)
# ---------------------------------------------------------------------------
FROM ${GO_IMAGE} AS dev

ARG AIR_VERSION=v1.67.4

ENV CGO_ENABLED=0 \
    GOTOOLCHAIN=local \
    GOPATH=/go \
    GOCACHE=/home/nonroot/.cache/go-build \
    GOTMPDIR=/home/nonroot/.cache/tmp \
    GOMODCACHE=/go/pkg/mod \
    HOME=/home/nonroot

# The cache mounts live outside /go and /home/nonroot so the chown below
# lands in the image layer (named volumes copy that ownership on first use).
RUN --mount=type=cache,id=bekup-gomod-dev,target=/root/gomod \
    --mount=type=cache,id=bekup-gobuild-dev,target=/root/.cache/go-build \
    GOMODCACHE=/root/gomod GOCACHE=/root/.cache/go-build GOTMPDIR= GOBIN=/usr/local/bin \
      go install github.com/air-verse/air@${AIR_VERSION} \
    && addgroup -S -g 65532 nonroot \
    && adduser -S -D -H -u 65532 -G nonroot -h /home/nonroot nonroot \
    && mkdir -p /home/nonroot/.cache/go-build /home/nonroot/.cache/tmp /go/pkg/mod /air \
    && chown -R 65532:65532 /home/nonroot /go /air

# Same unprivileged UID as the runtime image; the source is mounted read-only.
USER 65532:65532
WORKDIR /src
ENTRYPOINT ["air", "-c", ".air.toml"]

# ---------------------------------------------------------------------------
# Runtime stage (default): distroless static, nonroot, no shell, no package
# manager. Includes CA certificates and tzdata.
# ---------------------------------------------------------------------------
FROM ${RUNTIME_IMAGE} AS runtime

ARG VERSION=""
ARG VCS_REF=""
ARG BUILD_DATE=""

LABEL org.opencontainers.image.title="bekup" \
      org.opencontainers.image.description="Self-hosted, security-first backup manager for PostgreSQL, MySQL and MariaDB" \
      org.opencontainers.image.source="https://github.com/jlentink/bekup" \
      org.opencontainers.image.url="https://github.com/jlentink/bekup" \
      org.opencontainers.image.documentation="https://github.com/jlentink/bekup/tree/main/docs" \
      org.opencontainers.image.licenses="MIT" \
      org.opencontainers.image.vendor="bekup" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.revision="${VCS_REF}" \
      org.opencontainers.image.created="${BUILD_DATE}" \
      org.opencontainers.image.base.name="gcr.io/distroless/static-debian12:nonroot"

COPY --from=build --chown=65532:65532 /out/varlib /var/lib/bekup
COPY --from=build --chown=0:0 --chmod=0555 /out/bekup /usr/local/bin/bekup

# 65532 = distroless "nonroot". The binary is root-owned and read-only, so a
# compromised process cannot replace it even without read_only: true.
USER 65532:65532
WORKDIR /home/nonroot

# serve: 8080 (plain HTTP, internal network only, never published).
# runner: 7443 (mTLS gRPC, internal network only).
EXPOSE 8080 7443

# No default CMD: the subcommand (serve, runner, migrate, certs, ...) is always
# given explicitly, so an image can never start the wrong component by accident.
ENTRYPOINT ["/usr/local/bin/bekup"]
