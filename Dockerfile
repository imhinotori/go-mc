# Sulfur — Minecraft Java 26.2 (protocol 776) server, pure-Go static binary (no JVM).
# Multi-stage: build a CGO-disabled static binary, ship it on a minimal base.

# ---- build ----
FROM golang:1.26 AS build
WORKDIR /src

# Cache modules first.
COPY go.mod go.sum ./
RUN go mod download

# Build the server. CGO_ENABLED=0 keeps it a pure-Go static binary (the "no JVM" value
# prop) so it runs on a scratch/distroless base with no libc.
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/sulfur ./cmd/sulfur

# ---- runtime ----
# distroless static: no shell, no package manager, minimal attack surface; the static
# binary needs nothing but the kernel.
FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app
COPY --from=build /out/sulfur /app/sulfur

# The server listens on :25565 (the Minecraft Java port) by default (-addr).
EXPOSE 25565

# Runtime world saves / region dir land under the working dir; mount a volume there to
# persist a world across container updates.
USER nonroot:nonroot
ENTRYPOINT ["/app/sulfur"]
CMD ["-addr", ":25565"]
