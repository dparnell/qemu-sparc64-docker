# Pick and drive a container runtime; sourced by ../s10 and ../buildenv/sparc-build.
#
# RUNTIME=docker|podman|container chooses one. Otherwise: Apple container on Apple
# silicon macOS when installed (each container gets its own VM, and it runs arm64
# natively), else Docker or Podman, preferring one whose daemon/machine answers.

rt_die() { echo "${0##*/}: $*" >&2; exit 1; }

rt_pick() {
    if [ -n "$RUNTIME" ]; then
        case "$RUNTIME" in
        docker|podman|container) ;;
        *) rt_die "RUNTIME must be docker, podman or container, not '$RUNTIME'" ;;
        esac
        command -v "$RUNTIME" >/dev/null || rt_die "RUNTIME=$RUNTIME, but $RUNTIME is not installed"
        return
    fi
    if [ "$(uname -s)" = Darwin ] && [ "$(uname -m)" = arm64 ] && command -v container >/dev/null; then
        RUNTIME=container
        return
    fi
    for r in docker podman; do
        if command -v "$r" >/dev/null && "$r" info >/dev/null 2>&1; then
            RUNTIME=$r
            return
        fi
    done
    for r in docker podman; do
        command -v "$r" >/dev/null && { RUNTIME=$r; return; }
    done
    rt_die "no container runtime found: install Docker or Podman (or Apple container on macOS)"
}

# Make sure the runtime can run containers.
rt_ready() {
    case "$RUNTIME" in
    container)
        if ! container system status >/dev/null 2>&1; then
            echo "${0##*/}: starting Apple container's services" >&2
            container system start || rt_die "could not start Apple container (container system start)"
        fi
        ;;
    *)
        "$RUNTIME" info >/dev/null 2>&1 ||
            rt_die "$RUNTIME is installed but not answering; start it (Docker Desktop, the daemon, or podman machine start)"
        ;;
    esac
}

# rt_state NAME: running, stopped or absent.
rt_state() {
    case "$RUNTIME" in
    container)
        s=$(container inspect "$1" 2>/dev/null | sed -n 's/.*"state" *: *"\([a-z]*\)".*/\1/p' | head -n 1)
        ;;
    *)
        s=$("$RUNTIME" inspect -f '{{.State.Status}}' "$1" 2>/dev/null)
        ;;
    esac
    case "$s" in
    running) echo running ;;
    '') echo absent ;;
    *) echo stopped ;;
    esac
}

# A read-only bind mount is "$(rt_ro_flag)" "$(rt_ro_spec SRC DST)".
rt_ro_flag() { [ "$RUNTIME" = container ] && echo --mount || echo -v; }
rt_ro_spec() {
    if [ "$RUNTIME" = container ]; then
        echo "type=bind,source=$1,target=$2,readonly"
    else
        echo "$1:$2:ro"
    fi
}

rt_image_exists() { "$RUNTIME" image inspect "$1" >/dev/null 2>&1; }

# rt_vm_size MEM CPUS: Apple container gives each container a VM with 1 GiB of RAM by
# default; size it for the guest's RAM plus 1 GiB for QEMU itself, and a CPU more
# than the guest's for QEMU's I/O thread. Other runtimes share the host's memory.
rt_vm_size() {
    [ "$RUNTIME" = container ] || return 0
    case "$1" in
    *[Gg]) mb=$(( ${1%[Gg]} * 1024 )) ;;
    *[Mm]) mb=${1%[Mm]} ;;
    *) mb=$1 ;;
    esac
    case "$mb" in ''|*[!0-9]*) rt_die "can't read MEM=$1 (use e.g. 2G or 3072M)" ;; esac
    echo "-m $(( mb + 1024 ))M -c $(( $2 + 1 ))"
}
