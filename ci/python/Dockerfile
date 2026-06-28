# ci/python/Dockerfile — reproducible build image for the Phase-26 `-tags python`
# (opt-in CPython 3.14 embed) gate. The DEFAULT build (no -tags python) is pure-Go
# static CGO=0 and must NEVER need this image; this image exists ONLY to compile +
# link + test the gopy (gopython.xyz/py/v14) cgo lane, which needs:
#   - Python 3.14 dev headers + libpython3.14 + the `python-3.14-embed` pkg-config .pc
#   - gcc + make (cgo + -race instrumentation)
#   - libffi-dev (gopy's `#cgo pkg-config: python-3.14-embed libffi`)
#   - pkg-config (resolves the two .pc files at cgo time)
#   - Go 1.26 (matches the project toolchain; go.mod declares go 1.25.0, 1.26 is a
#     strict superset)
#
# Base: python:3.14-slim-bookworm already ships /usr/local/lib/libpython3.14.so and
# BOTH /usr/local/lib/pkgconfig/python-3.14.pc AND python-3.14-embed.pc — so the
# embed .pc the gopy preamble names is present out of the box. We only add the
# build toolchain (gcc/make/pkg-config/libffi-dev) and Go on top.
#
# Build:  docker build -t sulfur-py314 ci/python
# Use:    MSYS_NO_PATHCONV=1 docker run --rm \
#           -v //d/ender://src -w //src -v sulfur-gomod://go/pkg/mod \
#           sulfur-py314 go build -tags python ./...
FROM python:3.14-slim-bookworm

# Go toolchain version — keep in sync with the project (1.26 superset of go.mod's 1.25).
ARG GO_VERSION=1.26.0
ARG TARGETARCH=amd64

# Build toolchain for the cgo / -race lane + pkg-config + libffi.
# build-essential pulls gcc + make; pkg-config resolves python-3.14-embed.pc +
# libffi.pc at cgo time; libffi-dev provides libffi.pc + headers/lib.
RUN set -eux; \
    apt-get update; \
    apt-get install -y --no-install-recommends \
        build-essential \
        pkg-config \
        libffi-dev \
        ca-certificates \
        curl \
        git; \
    rm -rf /var/lib/apt/lists/*

# Install Go from the official tarball (the golang:1.26 apt repos are not on this base).
RUN set -eux; \
    curl -fsSL "https://go.dev/dl/go${GO_VERSION}.linux-${TARGETARCH}.tar.gz" -o /tmp/go.tgz; \
    tar -C /usr/local -xzf /tmp/go.tgz; \
    rm /tmp/go.tgz

ENV PATH="/usr/local/go/bin:/go/bin:${PATH}"
ENV GOPATH="/go"
# The gopy preamble is `#cgo pkg-config: python-3.14-embed libffi`; cgo must be on.
ENV CGO_ENABLED=1
# python:3.14 installs libpython under /usr/local/lib — make sure the dynamic linker
# finds libpython3.14.so.1.0 at test-binary run time (the .pc sets -L but not rpath).
ENV LD_LIBRARY_PATH="/usr/local/lib"

# Fail the build early if the embed .pc the gopy preamble names is not resolvable.
RUN set -eux; \
    pkg-config --exists python-3.14-embed; \
    pkg-config --exists libffi; \
    echo "python-3.14-embed: $(pkg-config --modversion python-3.14-embed)"; \
    echo "libffi:            $(pkg-config --modversion libffi)"; \
    go version

WORKDIR /src
