# Multi-stage build for the EveryAPI edge agent. ~12 MB final image —
# pure Go binary on a distroless base, no shell, no package manager.
# A compromised agent has no obvious lateral-movement primitives.

FROM golang:1.25-alpine AS build
WORKDIR /src

# Cache the module layer separately so `bun run dev`-style iteration
# on the source doesn't re-pull dependencies every build.
COPY go.mod go.sum ./
RUN go mod download

COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=linux \
    go build -trimpath -ldflags="-s -w -X main.Version=${VERSION}" \
    -o /out/everyapi-edge .

FROM gcr.io/distroless/static:nonroot

# OCI labels — GHCR reads `org.opencontainers.image.source` to decide
# which repo to link the package to in the UI (defaults to the
# pushing workflow's repo, which here is the monorepo). Pointing it
# at the public mirror surfaces the image on
# https://github.com/everyapi-ai/everyapi-edge's Packages sidebar
# and on the package page itself, which is where suppliers actually
# read from. `licenses` is the OCI standard tag for Apache-2.0.
ARG VERSION=dev
LABEL org.opencontainers.image.source="https://github.com/everyapi-ai/everyapi-edge" \
      org.opencontainers.image.description="EveryAPI BYO-GPU supplier agent — reverse-WS to gateway, forwards inference to local ollama." \
      org.opencontainers.image.licenses="Apache-2.0" \
      org.opencontainers.image.title="everyapi-edge" \
      org.opencontainers.image.version="${VERSION}"

COPY --from=build /out/everyapi-edge /everyapi-edge

# Identity volume — mount /var/lib/everyapi-edge to a host directory
# so the Ed25519 keypair survives container restarts. The gateway
# remembers the original pubkey; a fresh key on every restart would
# orphan the node row.
VOLUME ["/var/lib/everyapi-edge"]

USER nonroot:nonroot
ENTRYPOINT ["/everyapi-edge"]
