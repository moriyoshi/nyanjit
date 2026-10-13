#!/usr/bin/env python3
"""Fetch and verify pinned VM media in a separately cached Docker layer."""

import argparse
import gzip
import hashlib
import lzma
from pathlib import Path
import subprocess


KEY = Path("/usr/local/share/nyanjit/openbsd-79-base.pub")
MEDIA = Path("/work/media")


def run(*args, **kwargs):
    return subprocess.run(args, check=True, **kwargs)


def selected(arch, selection):
    return arch in {part.strip() for part in selection.split(",")}


def digest(path, algorithm, compression=None):
    hash_ = hashlib.new(algorithm)
    opener = {None: open, "gzip": gzip.open, "xz": lzma.open}[compression]
    with opener(path, "rb") as input_:
        while chunk := input_.read(1024 * 1024):
            hash_.update(chunk)
    return hash_.hexdigest()


def checked_download(url, path, algorithm, expected, compression=None):
    fetch(url, path)
    actual = digest(path, algorithm, compression)
    if actual != expected:
        raise RuntimeError(f"{path.name} {algorithm} {actual}, expected {expected}")


def fetch(url, path):
    path.parent.mkdir(parents=True, exist_ok=True)
    run("curl", "-fsSL", "--retry", "4", "--retry-delay", "3", "-o", str(path), url)


def openbsd(arch, manifest_hash, selection):
    if not selected(arch, selection):
        return
    media = MEDIA / arch
    media.mkdir(parents=True, exist_ok=True)
    base = f"https://cdn.openbsd.org/pub/OpenBSD/7.9/{arch}"
    sig = media / "SHA256.sig"
    fetch(f"{base}/SHA256.sig", sig)
    actual = hashlib.sha256(sig.read_bytes()).hexdigest()
    if actual != manifest_hash:
        raise RuntimeError(f"{arch} SHA256.sig hash {actual}, expected {manifest_hash}")
    boot_media = {
        "amd64": "cd79.iso", "i386": "cd79.iso",
        "arm64": "miniroot79.img", "riscv64": "miniroot79.img",
        "armv7": "bsd.rd", "powerpc64": "bsd.rd",
    }[arch]
    names = sorted({"bsd", "bsd.rd", "base79.tgz", "man79.tgz", "BUILDINFO", boot_media})
    if arch != "armv7":
        names.append("bsd.mp")
    if arch == "armv7":
        names.append("BOOTARM.EFI")
    if arch == "powerpc64":
        names.append("boot")
    for name in names:
        fetch(f"{base}/{name}", media / name)
    for name in names:
        run("signify-openbsd", "-C", "-p", str(KEY), "-x", "SHA256.sig", name, cwd=media)


def freebsd(arch, expected):
    filename = ("FreeBSD-15.1-RELEASE-amd64" if arch == "amd64" else
                "FreeBSD-15.1-RELEASE-arm64-aarch64") + "-BASIC-CLOUDINIT-ufs.qcow2.xz"
    directory = "amd64" if arch == "amd64" else "aarch64"
    url = f"https://download.freebsd.org/releases/VM-IMAGES/15.1-RELEASE/{directory}/Latest/{filename}"
    checked_download(url, MEDIA / f"freebsd-{arch}" / filename, "sha512", expected)


def netbsd(arch, expected):
    if arch == "amd64":
        filename = "NetBSD-11.0-amd64-live.img.gz"
        url = "https://cdn.netbsd.org/pub/NetBSD/images/11.0/" + filename
    else:
        directory, filename = {
            "arm64": ("evbarm-aarch64", "arm64mbr.img.gz"),
            "arm": ("evbarm-earmv7hf", "armv7.img.gz"),
        }[arch]
        url = f"https://cdn.netbsd.org/pub/NetBSD/NetBSD-11.0/{directory}/binary/gzimg/{filename}"
    # NetBSD's SHA512 files name the uncompressed image for evbarm.
    checked_download(url, MEDIA / f"netbsd-{arch}" / filename, "sha512", expected,
                     "gzip" if arch != "amd64" else None)


def dragonfly(expected):
    filename = "dfly-x86_64-6.4.2_REL.img.bz2"
    url = "https://mirror-master.dragonflybsd.org/iso-images/" + filename
    checked_download(url, MEDIA / "dragonfly-amd64" / filename, "md5", expected)


def omnios(expected):
    filename = "omnios-r151058.cloud.qcow2"
    url = "https://downloads.omnios.org/media/stable/" + filename
    checked_download(url, MEDIA / "omnios-amd64" / filename, "sha256", expected)


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("os", choices=("openbsd", "freebsd", "netbsd", "dragonfly", "omnios"))
    parser.add_argument("arch")
    parser.add_argument("--manifest-hash", required=True)
    parser.add_argument("--selection", required=True)
    parser.add_argument("--os-selection", default="openbsd", help="OpenBSD OS gate")
    args = parser.parse_args()
    if args.os == "openbsd":
        if not selected("openbsd", args.os_selection):
            return
        openbsd(args.arch, args.manifest_hash, args.selection)
    elif selected(args.os, args.selection):
        if args.os == "freebsd":
            freebsd(args.arch, args.manifest_hash)
        elif args.os == "netbsd":
            netbsd(args.arch, args.manifest_hash)
        elif args.os == "dragonfly":
            dragonfly(args.manifest_hash)
        else:
            omnios(args.manifest_hash)


if __name__ == "__main__":
    main()
