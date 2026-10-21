\ OpenBIOS (QEMU sun4u) device-tree fixes for Solaris 10, run from nvramrc.
\ The launcher drops the "\" comment lines and joins the rest into a single nvramrc string,
\ so keep comments on their own lines.
\
\ 1. power@0 has "interrupts" 1 but no matching ebus interrupt-map entry, so Solaris
\    falls back to the ebus INTA -> INO 0x21, which is shared with hme0. Every hme
\    interrupt then runs the power-button handler ("Power off requested ...").
" /pci@1fe,0/pci@1,1/ebus@1/power" find-device " interrupts" delete-property device-end
\ 1b. A real Ultra 5/10 PROM has the IDE master disk as disk@0,0 directly under ide@3
\    (reg <0 0>, compatible ide-disk -> dad). When mounting root, Solaris only configures
\    PROM-backed children of uata; OpenBIOS keeps its disk under an extra ide@0 channel
\    node, so add the real-hardware style node for Solaris (see fix 3). OpenBIOS itself
\    keeps booting through its own ide@0/disk@0.
" /pci@1fe,0/pci@1,1/ide@3" find-device new-device " disk" device-name " block" device-type 0 encode-int 0 encode-int encode+ " reg" property " ide-disk" encode-string " compatible" property finish-device device-end
\ 1e. The floppy node (fdthree) has a register specification Solaris rejects ("ebus:
\    WARNING: Out of range register specification from device node <fdthree>"); fd's
\    attach then fails and its cleanup dereferences NULL (panic in fd_cleanup). There is
\    no floppy drive here; Solaris ignores status "disabled" on it, so rename the node
\    and no driver binds to it.
" /pci@1fe,0/pci@1,1/ebus@1/fdthree" find-device " fdthree-disabled" device-name device-end
\ 1c. SMP: OpenBIOS creates a single CPU node whatever the CPU count. Read the count from
\    fw_cfg (selector port 510, data port 511, FW_CFG_NB_CPUS = 5) and clone the boot CPU
\    node for each further CPU, with cpuid/upa-portid set to its QEMU UPA port ID.
\    Does nothing with one CPU.
: (nb-cpus) ( -- n ) 5 510 iow! 511 ioc@ 511 ioc@ 8 lshift or ;
: (cp-props) ( src-ph -- ) >r 0 0 begin r@ next-property while 2dup r@ get-package-property 0= if 2over property then repeat r> drop ;
: (add-cpu) ( src-ph id -- ) " /" find-device new-device swap (cp-props) dup encode-int " cpuid" property encode-int " upa-portid" property finish-device ;
: (add-cpus) ( -- ) " /SUNW,UltraSPARC-IIi" find-dev if (nb-cpus) 1 max 1 ?do dup i (add-cpu) loop drop then device-end ;
(add-cpus)
\ 2. instance-to-path must return paths that match Solaris' own device names:
\    - Solaris names the host bridge pci@1f,0 (UPA port id), OpenBIOS uses pci@1fe,0;
\    - for the console ihandle OpenBIOS returns ".../ebus@1/su    @0" (padded, wrong
\      unit address); Solaris names the node su@14,3f8.
\    Otherwise consconfig cannot resolve stdin/stdout, falls back to the prom/wc console
\    and never opens the su driver, so serial input is dead.
\    Inside client-services "exit" is the client-interface exit service, not Forth's
\    exit, so these words must not use it.
" /openprom/client-services" find-device
create (i2p-buf) 100 allot
: (i2p-cpy) >r -rot dup r@ min swap over > if 2dup + 0 swap c! then move r> ;
: (i2p-fix) dup 8 > if over " /pci@1fe" drop 8 comp 0= if " /pci@1f" (i2p-buf) swap move 8 /string dup >r (i2p-buf) 7 + swap move (i2p-buf) r> 7 + then then ;
: instance-to-path ( buflen buf ihandle -- len ) dup 0= if 3drop -1 else >r swap r> dup ihandle>phandle " /pci@1fe,0/pci@1,1/ebus@1/su" find-dev if = else drop false then if drop " /pci@1f,0/pci@1,1/ebus@1/su@14,3f8" else get-instance-path (i2p-fix) then (i2p-cpy) then ;
\ 3. Booting the installed disk: /chosen bootpath is /pci@1fe,0/pci@1,1/ide@3/ide@0/disk@0:a.
\    The Solaris boot block (FCode run inside OpenBIOS) needs that OpenBIOS path to open
\    the disk, but the kernel cannot resolve it and fails to mount root. The kernel reads
\    bootpath through the client interface, so getprop/getproplen hand it the path a real
\    Ultra 5/10 PROM would give for the IDE master disk instead:
\    /pci@1f,0/pci@1,1/ide@3/disk@0,0:<slice> (uata maps disk@ to its dad child; asking
\    for dad@0,0 directly never enumerates it). CD boots are left alone.
\    Numbers here are hex unless prefixed with d#.
' getprop constant (old-getprop)
' getproplen constant (old-getproplen)
create (bp-buf) d# 40 allot
: (bp-orig) ( -- str len ) " bootpath" " /chosen" find-dev drop get-package-property if 0 0 else decode-string 2swap 2drop then ;
: (bp-disk?) ( -- flag ) (bp-orig) dup d# 37 > if drop " /pci@1fe,0/pci@1,1/ide@3/ide@0/disk@0" drop d# 37 comp 0= else 2drop false then ;
: (bp-req?) ( name phandle -- name phandle flag ) over cstrlen 8 = if over " bootpath" drop 8 comp 0= else false then if dup " /chosen" find-dev drop = else false then if (bp-disk?) else false then ;
: (bp-sol) ( -- addr len ) (bp-orig) + 1- c@ " /pci@1f,0/pci@1,1/ide@3/disk@0,0:a" (bp-buf) swap move (bp-buf) d# 33 + c! 0 (bp-buf) d# 34 + c! (bp-buf) d# 35 ;
: getproplen ( name phandle -- len|-1 ) (bp-req?) if 2drop d# 35 else (old-getproplen) execute then ;
: getprop ( buflen buf name phandle -- size|-1 ) (bp-req?) if 2drop (bp-sol) >r swap rot r> dup >r min move r> else (old-getprop) execute then ;
\    The kernel then looks that path up with finddevice, loads drivers for the node found
\    and its PROM parents, and attaches dad to it. OpenBIOS cannot resolve the Solaris
\    path (and its own ide@0 channel node has no Solaris driver), so map Solaris' disk
\    paths (disk@0,0 or dad@0,0, with or without :slice) to the disk node from fix 1b.
' finddevice constant (old-finddevice)
" /pci@1fe,0/pci@1,1/ide@3/disk" find-dev 0= if -1 then constant (sol-disk-ph)
: (pfx?) ( cstr str len -- cstr flag ) 2 pick cstrlen over < if 2drop false else >r over swap r@ comp 0= if dup r@ + c@ dup 0= swap [char] : = or else false then r> drop then ;
: (sol-disk?) ( cstr -- cstr flag ) " /pci@1f,0/pci@1,1/ide@3/disk@0,0" (pfx?) dup 0= if drop " /pci@1f,0/pci@1,1/ide@3/dad@0,0" (pfx?) then ;
: finddevice ( dev_spec -- phandle|-1 ) (sol-disk?) if drop (sol-disk-ph) else (old-finddevice) execute then ;
\ 3b. picld asks for the ASR (automatic system recovery) data through /dev/openprom.
\    OpenBIOS has no SUNW,asr-export-len, so the call fails without writing its result
\    and the openprom driver uses an uninitialised length; with 4G of RAM that garbage
\    makes it panic (BAD TRAP in opromioctl_cb). Report no ASR data. SUNW,asr-export
\    stays unimplemented: with a length of 0 its error return is handled cleanly.
: SUNW,asr-export-len ( -- len ) 0 ;
\ 3c. SMP: Solaris starts each further CPU (see fix 1c) with SUNW,start-cpu (node pc arg)
\    and panics if that fails. OpenBIOS has no parked secondary CPUs, so hand the request
\    to QEMU's CPU start register (ebus I/O 520: pc and arg as 32-bit halves, then the
\    UPA port ID at 530). QEMU starts that CPU at pc with %o0 = arg, using the calling
\    CPU's MMU state and trap table.
: (cpu-start) ( arg pc portid -- ) >r dup 520 iol! 20 rshift 524 iol! dup 528 iol! 20 rshift 52c iol! r> 530 iol! ;
: SUNW,start-cpu ( arg pc node -- ) " upa-portid" rot get-package-property if 0 else decode-int nip nip then (cpu-start) ;
\ 3d. Each CPU's MMU setup interprets "' unix-tte is va>tte-data", pointing OBP's
\    translation lookup hook at the kernel's Forth word unix-tte. Solaris never defines
\    that word here, so the command fails with -13 ("unix-tte:interpret: exception -13")
\    and the hook keeps the kernel's kmem64-tte, which OpenBIOS does use. The boot CPU
\    survives that error, but a secondary CPU crashes in OpenBIOS's error path (BAD TRAP
\    in the Forth inner interpreter), so skip exactly this command; same end result.
' interpret constant (old-interpret)
: (tte-cmd?) ( cmd -- cmd flag ) dup cstrlen d# 25 = if dup " ' unix-tte is va>tte-data" drop d# 25 comp 0= else false then ;
: interpret ( ... cmd -- ... catch-result ) (tte-cmd?) if drop 0 else (old-interpret) execute then ;
device-end
\ 4. OpenBIOS normally leaves /chosen as the active package, so the Forth that Solaris
\    feeds through "interpret" is compiled into /chosen's wordlist. Restore that; leaving
\    "forth" current makes Solaris redefine the global "sync" and OpenBIOS then crashes.
" /chosen" find-device
