# qemu-sparc64-docker

A small Alpine image with `qemu-system-sparc64` (sun4u) and `qemu-img`, built from
[dparnell/qemu `sun4u-solaris10`](https://github.com/dparnell/qemu/tree/sun4u-solaris10),
for running Solaris 10 1/13 (SPARC).

The fork carries two IDE fixes Solaris needs:

- **CMD646 interrupt status latching**: without it Solaris' `uata` driver logs
  "Interrupt not seen after set_features" and no disk or CD-ROM attaches.
- **ATAPI media change**: report only UNIT ATTENTION when a disc is loaded. Without it
  the first open of the DVD fails, and the installer reports "not a Solaris OS DVD"
  and ejects the disc.

`openbios-fixes.fth` patches the OpenBIOS device tree at boot (loaded as `nvramrc`):

- the console path, so serial input works;
- the spurious power-button interrupt;
- the boot path and disk node, so the installed system can mount root.

## Build

```sh
docker build -t qemu-sparc64-solaris .
```

Build arguments: `QEMU_REPO` (default the fork over https), `QEMU_REF` (branch or
tag, default `sun4u-solaris10`), `ALPINE_VERSION`.

## Use

Put the disk image and DVD ISO in a directory and mount it at `/vm`. Use `-it`: the
Solaris console is the container's terminal. Quit QEMU with Ctrl-A then X.

```sh
# create a disk
docker run --rm -v "$PWD:/vm" qemu-sparc64-solaris qemu-img create -f qcow2 s10.qcow2 64G

# install from the DVD
docker run --rm -it -v "$PWD:/vm" qemu-sparc64-solaris install

# boot the installed system; guest ssh on localhost:2222, Oracle listener on :1521
docker run --rm -it -v "$PWD:/vm" -p 2222:2222 -p 1521:1521 qemu-sparc64-solaris
```

Environment variables (`-e`):

| Variable | Default | Meaning |
|---|---|---|
| `DISK` | `s10.qcow2` | disk image in `/vm` |
| `ISO` | `sol-10-u11-ga-sparc-dvd.iso` | installer DVD for `install` |
| `CDROM` | | ISO to attach in `run` mode (e.g. Oracle media) |
| `MEM` | `2G` | guest memory |
| `NET` | user-mode NIC with the two port forwards | QEMU `-nic` spec |

Extra arguments after `run` or `install` are passed to QEMU, and any other command is
run as-is. In the installer, answer DHCP for `hme0`; DNS is `10.0.2.3`.
