# Speklo API server → ghcr.io/<owner>/<repo>/api:<version>
# Build context: server/api

# Build on the runner's own architecture and cross-compile (no emulation).
FROM --platform=$BUILDPLATFORM golang:1.26 AS build
ARG TARGETOS TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath \
      -ldflags "-s -w -X github.com/anilcharles/speklo/server/api/internal/api.Version=${VERSION}" \
      -o /out/speklo ./cmd/speklo

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/speklo /speklo
ENV SPEKLO_ENV=prod \
    SPEKLO_ADDR=:8080
EXPOSE 8080
USER nonroot
ENTRYPOINT ["/speklo"]
