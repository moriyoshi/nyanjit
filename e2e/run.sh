#!/bin/sh
set -eu
repo=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
image=${NYANJIT_E2E_IMAGE:-nyanjit-e2e:local}
arch=$(uname -m)
case "$arch" in aarch64) arch=arm64;; x86_64) arch=amd64;; esac
platform=${NYANJIT_E2E_PLATFORM:-linux/$arch}
# Use '-' so an explicitly empty selection stays empty.
vm_oses=${NYANJIT_E2E_VM_OSES-openbsd,freebsd,netbsd,dragonfly,omnios}
openbsd_archs=${NYANJIT_E2E_OPENBSD_ARCHS-amd64,i386,arm64,riscv64,armv7,powerpc64}
if [ -z "${NYANJIT_E2E_IMAGE:-}" ]; then
    docker build --platform "$platform" \
      --build-arg "VM_OSES=$vm_oses" --build-arg "OPENBSD_ARCHS=$openbsd_archs" \
      --build-arg "LINUX_ONLY=${NYANJIT_E2E_LINUX_ONLY:-false}" \
      -f "$repo/e2e/container/Dockerfile" -t "$image" "$repo"
fi
script=${1:-e2e/matrix.star}
if [ "$#" -gt 0 ]; then shift; fi
set -- "$image" nyanjit-e2e "$@" "$script"
if [ -e /dev/kvm ]; then set -- --device /dev/kvm "$@"; fi
# A bind directory is useful for CI artifacts; the default keeps local caches.
exec docker run --rm --privileged \
  -e "NYANJIT_E2E_VAR_SWEEP=${NYANJIT_E2E_VAR_SWEEP:-2000}" \
  -e GOCACHE=/cache/go-build -e GOMODCACHE=/cache/go-mod \
  -v "$repo:/src" -v "${NYANJIT_E2E_CACHE:-nyanjit-e2e-cache}:/cache" "$@"
