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
- the boot path and disk node, so the installed system can mount root;
- a missing `SUNW,asr-export-len` service, without which `picld` makes the kernel panic
  at boot with 4G of RAM.

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

### Settings

Pass these with `-e NAME=value`:

| Variable | Default | Meaning |
|---|---|---|
| `DISK` | `s10.qcow2` | disk image in `/vm` |
| `ISO` | `sol-10-u11-ga-sparc-dvd.iso` | installer DVD for `install` |
| `CDROM` | | ISO to attach in `run` mode (e.g. Oracle media) |
| `MEM` | `2G` | guest RAM; up to `4G` works |
| `CPU` | `TI-UltraSparc-IIi` | CPU model (`qemu-system-sparc64 -cpu help`) |
| `CPUS` | `1` | CPU count; QEMU's sun4u machine supports only 1 |
| `NET_MODE` | `user` | `user` (NAT with port forwards) or `none` |
| `PORTS` | `2222:22 1521:1521` | forwards `[tcp:\|udp:]container:guest`, space or comma separated; empty for none |
| `MAC` | QEMU default | guest MAC address, e.g. `52:54:00:12:34:57` for a second VM |
| `NET_OPTS` | | extra user-mode options, e.g. `net=10.0.3.0/24` |
| `NET` | | full QEMU `-nic` spec; overrides `NET_MODE`/`PORTS`/`MAC`/`NET_OPTS` |
| `QEMU_OPTS` | | extra QEMU arguments |
| `DRY_RUN` | | `1` prints the QEMU command instead of running it |

Extra arguments after `run` or `install` are also passed to QEMU, and any other command
is run as-is.

Notes:

- **CPUs:** QEMU emulates sun4u as a single-CPU machine (`-smp 2` fails with "max CPUs
  supported by machine 'sun4u' is 1"). Emulation runs on one host thread either way.
- **Memory:** 2G, 3G and 4G boot to login. Above 4G (even 4352M) OpenBIOS itself
  crashes at power-on ("Unhandled Exception 0x30"), so 4G is the maximum until the
  OpenBIOS ROM is fixed. The launcher warns above 4G.
- **Networking:** with `NET_MODE=user` the guest gets `10.0.2.15` by DHCP (gateway
  `10.0.2.2`, DNS `10.0.2.3`) and can reach out, but nothing can connect in except the
  `PORTS` forwards. Each forward listens on the container port; publish it with `-p`,
  e.g. `-e PORTS="2222:22 8080:80" -p 2222:2222 -p 8080:8080`. In the installer, answer
  DHCP for `hme0`.

Example, a second VM with its own disk, MAC and ports:

```sh
docker run --rm -it -v "$PWD:/vm" -e DISK=s10-b.qcow2 -e MAC=52:54:00:12:34:57 \
    -e PORTS="2223:22 1522:1521" -p 2223:2223 -p 1522:1522 qemu-sparc64-solaris
```
