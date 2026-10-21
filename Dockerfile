# syntax=docker/dockerfile:1
#
# qemu-system-sparc64 (sun4u) for running Solaris 10, built from the patched fork.
# Stage 1 builds QEMU from GitHub; stage 2 keeps only the binaries, the sparc64
# OpenBIOS ROM and the shared libraries they need.

ARG ALPINE_VERSION=3.20

FROM alpine:${ALPINE_VERSION} AS build

ARG QEMU_REPO=https://github.com/dparnell/qemu.git
ARG QEMU_REF=sun4u-solaris10

RUN apk add --no-cache \
        bash bison build-base flex git glib-dev libslirp-dev linux-headers \
        perl pixman-dev pkgconf python3 samurai scanelf zlib-dev

RUN git clone --depth 1 --branch "${QEMU_REF}" "${QEMU_REPO}" /src/qemu

WORKDIR /src/qemu/build
RUN ../configure \
        --prefix=/usr \
        --target-list=sparc64-softmmu \
        --without-default-features \
        --enable-slirp \
        --enable-tools \
        --disable-docs \
    && ninja qemu-system-sparc64 qemu-img

# Stage the runtime payload: the two binaries, the one firmware blob sun4u uses, and
# the list of shared libraries they link (as apk "so:" names for the next stage).
RUN mkdir -p /out/usr/bin /out/usr/share/qemu \
    && install -s qemu-system-sparc64 qemu-img /out/usr/bin/ \
    && install -m 0644 ../pc-bios/openbios-sparc64 /out/usr/share/qemu/ \
    && scanelf --needed --nobanner --format '%n#p' /out/usr/bin/* \
        | tr ',' '\n' | sort -u | sed 's/^/so:/' > /out/runtime-deps \
    && git -C .. log -1 --format='%H %s' > /out/usr/share/qemu/BUILD-REVISION

FROM alpine:${ALPINE_VERSION}

COPY --from=build /out/runtime-deps /tmp/runtime-deps
RUN apk add --no-cache $(cat /tmp/runtime-deps) && rm /tmp/runtime-deps

COPY --from=build /out/usr/ /usr/
COPY openbios-fixes.fth /usr/share/qemu/openbios-fixes.fth
COPY --chmod=0755 solaris-sparc64 /usr/bin/solaris-sparc64

# Disk images and ISOs live in /vm. By default the guest's ssh and Oracle listener are
# forwarded to these container ports (publish them with -p; change them with PORTS).
WORKDIR /vm
VOLUME /vm
EXPOSE 2222 1521

ENTRYPOINT ["/usr/bin/solaris-sparc64"]
CMD ["run"]
