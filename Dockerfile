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
COPY --from=build /out/everyapi-edge /everyapi-edge

# Identity volume — mount /var/lib/everyapi-edge to a host directory
# so the Ed25519 keypair survives container restarts. The gateway
# remembers the original pubkey; a fresh key on every restart would
# orphan the node row.
VOLUME ["/var/lib/everyapi-edge"]

USER nonroot:nonroot
ENTRYPOINT ["/everyapi-edge"]
