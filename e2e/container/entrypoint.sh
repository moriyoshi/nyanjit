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
case "${NYANJIT_E2E_LOONG64_QEMU:-tonistiigi}" in
    tonistiigi) ;;
    debian)
        python3 /usr/local/bin/nyanjit-e2e-binfmt.py /opt/nyanjit/qemu-debian/qemu-loongarch64
        /opt/nyanjit/qemu-debian/qemu-loongarch64 --version
        ;;
    *) echo "unknown NYANJIT_E2E_LOONG64_QEMU: $NYANJIT_E2E_LOONG64_QEMU" >&2; exit 2;;
esac
mkdir -p /cache/results /cache/vms
exec "$@"
