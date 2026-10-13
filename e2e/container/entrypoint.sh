#!/bin/sh
set -eu
# The caller grants --privileged. A fresh user+mount namespace gives this
# container its own binfmt_misc without changing the host's registration.
if [ "${NYANJIT_E2E_BINFMT_READY:-}" != "1" ]; then
    export NYANJIT_E2E_BINFMT_READY=1
    exec unshare -Urm --propagation private "$0" "$@"
fi
mount -t binfmt_misc binfmt_misc /proc/sys/fs/binfmt_misc
# The tonistiigi binaries expect an extra original argv[0]; match their
# preserve-argv0 patch with the kernel P flag. F keeps the interpreter open.
QEMU_PRESERVE_ARGV0=1 /usr/bin/binfmt --install all >/dev/null
# tonistiigi's image has no big-endian ppc64 emulator. Debian supplies one;
# register it in this same private namespace with F semantics.
if [ -f /usr/lib/binfmt.d/qemu-ppc64.conf ]; then
    sed -e 's#/usr/libexec/qemu-binfmt/ppc64-binfmt-P#/usr/bin/qemu-ppc64-static#' \
        -e 's/:OPF$/:F/' /usr/lib/binfmt.d/qemu-ppc64.conf \
        > /proc/sys/fs/binfmt_misc/register
fi
mkdir -p /cache/results /cache/vms
exec "$@"
