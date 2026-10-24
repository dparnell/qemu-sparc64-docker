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

and multi-CPU support for the sun4u machine (`-smp` up to 4): CPU creation, a CPU start
register the firmware's `SUNW,start-cpu` uses, UltraSPARC-II cross-CPU interrupts
(mondos), and fixes needed to run the CPUs on separate host threads (MTTCG).

`openbios-fixes.fth` patches the OpenBIOS device tree at boot (loaded as `nvramrc`):

- the console path, so serial input works;
- the spurious power-button interrupt;
- the boot path and disk node, so the installed system can mount root;
- a missing `SUNW,asr-export-len` service, without which `picld` makes the kernel panic
  at boot with 4G of RAM;
- the floppy node, whose bad register specification makes Solaris' `fd` driver panic;
- for multiple CPUs: a CPU node per CPU, the `SUNW,start-cpu` service, and a workaround
  for a Forth command that crashes OpenBIOS on a secondary CPU.

## Build

```sh
docker build -t qemu-sparc64-solaris .
```

Build arguments: `QEMU_REPO` (default the fork over https), `QEMU_REF` (branch or
tag, default `sun4u-solaris10`), `ALPINE_VERSION`.

## Build environment

`buildenv/` holds a toolchain image (Ubuntu 24.04 with QEMU's build dependencies, the
`sparc64-linux-gnu` cross compiler for OpenBIOS, `xsltproc` and `gdb-multiarch`), so
nothing has to be installed on the host. `buildenv/sparc-build` runs a command in it as
your user, with `$HOME` (and the current directory, if outside it) mounted at the same
path; it builds the image on first use.

```sh
cd ~/fork/qemu/build && ~/fork/qemu-sparc64-docker/buildenv/sparc-build ninja qemu-system-sparc64
~/fork/qemu-sparc64-docker/buildenv/sparc-build bash     # shell
SPARC_BUILD_REBUILD=1 ~/fork/qemu-sparc64-docker/buildenv/sparc-build true   # rebuild image
```

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

### macOS (Apple silicon) with Apple `container`

The image builds natively for arm64 with Apple's
[`container`](https://github.com/apple/container) tool, which runs each container in its
own lightweight VM. Its commands mirror Docker's. Each container VM gets 1 GiB of RAM by
default, so give it more than the guest's `MEM` with `-m`, and use `-c` for CPUs:

```sh
brew install container
brew services start container
container system kernel set --recommended     # once

container build -t qemu-sparc64-solaris .
container run --rm -v "$PWD:/vm" qemu-sparc64-solaris qemu-img create -f qcow2 s10.qcow2 64G
container run --rm -it -m 3G -c 2 -v "$PWD:/vm" qemu-sparc64-solaris install
container run --rm -it -m 3G -c 2 -v "$PWD:/vm" -p 2222:2222 -p 1521:1521 qemu-sparc64-solaris
```

With `MEM=4G` use `-m 5G` or more.

### Session mode: run in the background, attach to the console

`session` (or `session install`) runs QEMU in a tmux session inside the container, so
the VM keeps running with nobody attached and you can attach and detach at will:

```sh
container run -d --name s10 -m 3G -c 2 -v "$PWD:/vm" \
    -p 127.0.0.1:2222:2222 -p 127.0.0.1:1521:1521 qemu-sparc64-solaris session
container exec -it s10 attach            # Ctrl-B d detaches, Ctrl-B m opens the menu
container exec s10 vmctl status          # or any other vmctl command, see vmctl help
```

(With Docker: `docker run -d ... session` and `docker exec -it s10 attach`.)

- **Console:** window `console` is the guest's serial console. Every key goes to the
  guest, Ctrl-C, Ctrl-Z and Ctrl-A included; nothing typed there can stop QEMU. Choose
  "DEC VT100" or "xterm" as the terminal type in the installer. The console stays
  80x24 whatever the terminal size, since a serial console can't tell Solaris its
  size; "Console fit to terminal" in the menu resizes it, then set the size in Solaris
  with `stty rows R columns C`. Output is also appended to `/vm/console.log`
  (`CONSOLE_LOG`, empty for none).
- **Menu (Ctrl-B m):** change or eject the CD (lists the `.iso`/`.img` files in `/vm`),
  send a break, pause/resume, disk snapshots, console size, status, a `vmctl` shell,
  restart, power button and quit.
- **vmctl:** the same actions from the command line, through QEMU's QMP socket
  (`/run/qemu/qmp.sock`; `vmctl qmp '<json>'` sends raw QMP). It works in plain `run`
  mode too, except `restart`.
- **Restart** is a power cycle: QEMU quits and the session starts it again, keeping the
  disc in the drive and any attached terminals. QEMU's own reset crashes OpenBIOS once
  Solaris has run, so there is no warm reset.
- **Snapshots** are of the disk only, kept inside the qcow2 (`qemu-img snapshot -l`
  lists them too). Saving works while the VM runs, but take them with Solaris shut down
  to the `ok` prompt (`init 0`) for a clean file system. Restoring is a power cycle back
  to the snapshot. Snapshots with VM state (QEMU's savevm/loadvm) don't work here: the
  restored Solaris hangs.
- **Break** sends a serial break on ttya (Stop-A on a real Sun). Solaris 10 here
  ignores it, even after `kbd -a enable`, so it does not get you to the `ok` prompt.
- **CD changes:** Solaris sees a new disc at once (vold mounts it under `/cdrom`).
  OpenBIOS only sees the disc that was in the drive at power-on, so to boot from a
  different disc, change it and restart.
- **Web console:** the session also serves the console and the same controls on port
  8080 (see below).
- **Stopping:** shut Solaris down first (`init 0`, to the `ok` prompt), then quit QEMU
  from the menu or `container stop`. Stopping the container quits QEMU at once, like
  pulling the plug.

### Web console

In session mode `vmweb` serves a browser console on port 8080 (`WEB_PORT`, empty to
turn it off): the serial console in a terminal (xterm.js) plus buttons for the CD,
power, disk snapshots, break and console size. Publish the port on localhost and open
<http://localhost:8080/>:

```sh
container run -d --name s10 -m 3G -c 2 -v "$PWD:/vm" -p 127.0.0.1:2222:2222 \
    -p 127.0.0.1:8080:8080 qemu-sparc64-solaris session
```

It is the same tmux session as `attach`, through tmux control mode, so a terminal and
any number of browsers can share the console; a browser that connects or reconnects
starts from a capture of the screen and scrollback. Keys go to the guest as typed,
Ctrl-C included (browser shortcuts such as Cmd-W still belong to the browser).

The console has no login of its own and gives root on the guest's console, so keep it
on localhost. Apple `container` also makes the container's own IP reachable from the
Mac, so set `WEB_TOKEN` to require a token: open `http://localhost:8080/?token=...`
once and a cookie keeps you in. Without a token it only answers to `localhost` and IP
addresses (add host names with `WEB_HOSTS`), against DNS rebinding.

### Settings

Pass these with `-e NAME=value`:

| Variable | Default | Meaning |
|---|---|---|
| `DISK` | `s10.qcow2` | disk image in `/vm` |
| `ISO` | `sol-10-u11-ga-sparc-dvd.iso` | installer DVD for `install` |
| `CDROM` | | ISO in the CD drive in `run` mode (e.g. Oracle media); empty drive otherwise |
| `MEM` | `2G` | guest RAM; up to `4G` works |
| `CPU` | `TI-UltraSparc-IIi` | CPU model (`qemu-system-sparc64 -cpu help`) |
| `CPUS` | `1` | CPU count, up to 4 (2 is tested) |
| `NET_MODE` | `user` | `user` (NAT with port forwards) or `none` |
| `PORTS` | `2222:22 1521:1521` | forwards `[tcp:\|udp:]container:guest`, space or comma separated; empty for none |
| `MAC` | QEMU default | guest MAC address, e.g. `52:54:00:12:34:57` for a second VM |
| `NET_OPTS` | | extra user-mode options, e.g. `net=10.0.3.0/24` |
| `NET` | | full QEMU `-nic` spec; overrides `NET_MODE`/`PORTS`/`MAC`/`NET_OPTS` |
| `CONSOLE_LOG` | `console.log` | console log in session mode; empty for none |
| `WEB_PORT` | `8080` | web console port in session mode; empty for none |
| `WEB_TOKEN` | | token the web console requires |
| `WEB_HOSTS` | | extra host names the web console answers to without a token |
| `QEMU_OPTS` | | extra QEMU arguments |
| `DRY_RUN` | | `1` prints the QEMU command instead of running it |

Extra arguments after `run` or `install` are also passed to QEMU, and any other command
is run as-is.

Notes:

- **CPUs:** with `CPUS=2` Solaris brings both CPUs online, each emulated on its own host
  thread, so separate processes run in parallel; each CPU is still single-core TCG
  speed. Tested with 2 (boot to login and 10 minutes idle after it); 3 and 4 are
  accepted but untested. Add `-accel tcg,thread=single` to `QEMU_OPTS` to run all CPUs
  on one host thread instead.
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
