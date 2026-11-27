# Requara server → ghcr.io/<owner>/<repo>:<version>, e.g. ghcr.io/anilcharles/requara:0.1.1
# Build context: server/
#
# One image, one Go program: it serves the API, sign-in, mock servers and the
# web UI from the same origin. Everything that runs is memory-safe Go on a
# distroless base (no shell, no package manager, no C web server).
#
# Downloaded packages and compiled Go packages go into BuildKit cache mounts.
# A builder that keeps its state (CI's, or your own machine's) then fetches
# and compiles only what changed. Cache mounts never end up in the image.

# 1. Build the React UI. The output is plain files, so build it once on the
#    runner's own architecture. Debian rather than Alpine: musl's resolver
#    gives up (EAI_AGAIN) behind some VM DNS forwarders, such as colima's on
#    the self-hosted runner, and npm ci then crashes.
FROM --platform=$BUILDPLATFORM node:22-slim AS web
WORKDIR /web
COPY web/package.json web/package-lock.json ./
RUN --mount=type=cache,target=/root/.npm npm ci --no-audit --no-fund
COPY web/ .
RUN npm run build

# 2. Build the Go server, cross-compiling for the target platform.
FROM --platform=$BUILDPLATFORM golang:1.26 AS api
ARG TARGETOS TARGETARCH
WORKDIR /src
COPY api/go.mod api/go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY api/ .
ARG VERSION=dev
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath \
      -ldflags "-s -w -X github.com/anilcharles/requara/server/api/internal/api.Version=${VERSION}" \
      -o /out/requara ./cmd/requara

# 3. The personal workspace's engine: the same Go code compiled to
#    WebAssembly, which the web app runs in the browser (cmd/requara-wasm).
FROM --platform=$BUILDPLATFORM golang:1.26 AS core
WORKDIR /src
COPY api/go.mod api/go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY api/ .
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    mkdir -p /out/core \
 && GOOS=js GOARCH=wasm go build -trimpath -ldflags "-s -w" -o /out/core/requara-core.wasm ./cmd/requara-wasm \
 && cp "$(go env GOROOT)/lib/wasm/wasm_exec.js" /out/core/ \
 && gzip -9 -k /out/core/requara-core.wasm

# 4. The image: the Go binary plus the built UI.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=api /out/requara /requara
COPY --from=web /web/dist /web
COPY --from=core /out/core/ /web/core/
ENV REQUARA_ENV=prod \
    REQUARA_ADDR=:8080 \
    REQUARA_STATIC_DIR=/web
EXPOSE 8080
USER nonroot
ENTRYPOINT ["/requara"]
