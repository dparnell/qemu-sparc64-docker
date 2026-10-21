# Build environment for the patched QEMU and the sparc64 OpenBIOS ROM, so nothing has
# to be installed on the host. Use it through ./sparc-build (see README).
FROM ubuntu:24.04

ARG DEBIAN_FRONTEND=noninteractive
RUN apt-get update && apt-get install -y --no-install-recommends \
        # QEMU
        bison build-essential ca-certificates flex git libglib2.0-dev libpixman-1-dev \
        libslirp-dev meson ninja-build pkg-config python3 python3-venv zlib1g-dev \
        # OpenBIOS for sparc64
        fcode-utils gcc-sparc64-linux-gnu xsltproc \
        # inspecting and debugging firmware and guests
        file gdb-multiarch xxd \
    && rm -rf /var/lib/apt/lists/*
