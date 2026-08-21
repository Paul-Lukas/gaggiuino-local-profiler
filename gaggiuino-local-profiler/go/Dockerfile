# Go rewrite Docker image (#901 Phase 4, build-only CI infrastructure — see
# go/README.md). NOT wired into any release/dev channel yet: this Dockerfile
# exists so the CI workflow (.github/workflows/go-build.yaml) and a
# maintainer can prove the Go binary actually containerizes and runs, ahead
# of the still-undecided Phase 5 cutover. The repo-root Dockerfile (Node)
# remains the only image any real install pulls.
#
# Far simpler than the Node Dockerfile: one build stage instead of three
# (builder + prod-deps + runtime there), because there is no separate
# frontend build, no npm prod-dependency stage, and no better-sqlite3 native
# module to rebuild per target arch — modernc.org/sqlite is pure Go (no
# CGo), and every template/static asset is compiled into the binary via
# embed.FS (see internal/web/assets.go), so nothing besides the binary
# itself ships in the runtime stage.
#
# Multi-arch strategy: native Go cross-compilation, not QEMU emulation, for
# the expensive step. `--platform=$BUILDPLATFORM` below pins the builder
# stage to the CI runner's own architecture (amd64) regardless of which
# target platform buildx is assembling, so `go build` itself always runs
# natively — GOOS/GOARCH/GOARM cross-compile the actual target binary
# without ever executing target-arch code. That's the speed win #901 Phase
# 4 exists to prove: the Node image's slow part (compiling better-sqlite3's
# C++ addon under QEMU for arm64/armv7, see the repo-root Dockerfile's
# prod-deps stage comment) has no equivalent here at all. The one place a
# few seconds of QEMU emulation can still happen is the runtime stage's own
# `apk add` below, which — unlike a C++ compile — installs three tiny
# prebuilt packages; docker/setup-qemu-action is kept in the CI workflow
# for exactly that (and only that) reason.
FROM --platform=$BUILDPLATFORM golang:1.25-alpine AS builder
ARG TARGETOS
ARG TARGETARCH
ARG TARGETVARIANT

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

# templ generate is a required codegen step — internal/web/templates'
# .templ sources aren't valid Go until this runs (see go/README.md's
# "Codegen" section). Pinned to the same version go.mod already pulls in as
# the templ *runtime* library's indirect dependency, so the codegen tool and
# the library it generates code against never drift apart.
RUN go install github.com/a-h/templ/cmd/templ@v0.3.1020

COPY . .
RUN templ generate

RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH GOARM=${TARGETVARIANT#v} \
    go build -trimpath -ldflags="-s -w" -o /out/glp-server ./cmd/server

# Runtime — Alpine, not scratch/distroless: modernc.org/sqlite needs no CGo
# so there's no glibc/musl runtime dependency pulling this choice either
# way, but the container still needs a real shell plus a privilege-drop
# tool to keep the same chown-/data-then-run-unprivileged entrypoint
# pattern the Node image uses (docker-entrypoint.sh at the repo root, #271)
# — distroless and scratch have neither, and embed.FS removes the only
# other reason a runtime asset directory might be needed. Alpine also gives
# HEALTHCHECK a plain `wget`, identical to the Node image's, and ships a CA
# bundle for the outbound HTTPS calls to Open Food Facts (barcode scan) and
# GitHub (update check) — the concrete case the task brief called out
# scratch failing on.
FROM alpine:3.22

# /data is created (not just relied on as a bind-mount point) so the image
# also runs standalone without an explicit `-v` — matching the HA add-on's
# real usage, where Supervisor always provides the mount, but keeping local
# `docker run` (and this package's own scripts/smoke-test.sh in docker
# mode) simple. docker-entrypoint.sh's chown -R still re-fixes ownership
# every start for the real bind-mount case, whose host-side UID isn't known
# at build time.
RUN apk add --no-cache wget ca-certificates su-exec \
    && adduser -D -H -u 1000 glp \
    && mkdir -p /data \
    && chown glp:glp /data

COPY --from=builder /out/glp-server /usr/local/bin/glp-server
COPY docker-entrypoint.sh /usr/local/bin/
RUN chmod +x /usr/local/bin/docker-entrypoint.sh

EXPOSE 8099

# /web/static/style.css, not / (which has no registered route yet — the Go
# frontend's entry points are /shots, /beans, etc., not / itself, and
# hitting an unmapped path 404s wget --spider into a false "unhealthy"): a
# vendored static asset that's always 200 without a token (see
# internal/web/handlers.go's GET /web/static/ registration), so this only
# proves the HTTP server itself answers, matching the Node healthcheck's
# actual intent without depending on DB/auth state.
HEALTHCHECK --interval=30s --timeout=5s --start-period=15s --retries=3 \
    CMD wget -q --spider http://localhost:8099/web/static/style.css || exit 1

# Same as the Node image: starts as root (needed to chown the /data bind
# mount, see docker-entrypoint.sh) and immediately drops to the
# unprivileged `glp` user before running the actual app.
ENTRYPOINT [ "docker-entrypoint.sh" ]
CMD [ "glp-server" ]
