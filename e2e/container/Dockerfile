# syntax=docker/dockerfile:1.7
FROM --platform=$TARGETPLATFORM golang:1.26.9 AS go126
FROM --platform=$TARGETPLATFORM golang:1.27.2 AS go127
FROM --platform=$TARGETPLATFORM tonistiigi/binfmt:qemu-v10.2.3 AS binfmt
FROM --platform=linux/amd64 debian:trixie AS libc-amd64
FROM --platform=linux/386 debian:trixie AS libc-i386
FROM --platform=linux/arm/v7 debian:trixie AS libc-armhf
FROM --platform=linux/ppc64le debian:trixie AS libc-ppc64le
FROM --platform=linux/riscv64 debian:trixie AS libc-riscv64
# musl.cc's GCC 11.2.1 cross toolchain provides a big-endian ELFv2 libc.
# Archive fetched from the upstream publisher; pin bytes before extraction.
FROM --platform=$BUILDPLATFORM debian:trixie AS libc-ppc64-elfv2
ARG PPC64_MUSL_CROSS_SHA256=4ae497aa188d336b25872d32d586cb2d7afb8a153001deac50149babb9886bc8
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates curl python3 \
    && rm -rf /var/lib/apt/lists/*
RUN curl -fL --retry 4 -o /tmp/cross.tgz https://musl.cc/powerpc64-linux-musl-cross.tgz && \
    echo "$PPC64_MUSL_CROSS_SHA256  /tmp/cross.tgz" | sha256sum -c - && \
    mkdir -p /opt/nyanjit/toolchains && \
    tar xzf /tmp/cross.tgz -C /opt/nyanjit/toolchains && rm /tmp/cross.tgz
# e_flags=2 identifies ELFv2; EI_DATA=2 verifies the byte order.
RUN python3 -c 'import struct; p="/opt/nyanjit/toolchains/powerpc64-linux-musl-cross/powerpc64-linux-musl/lib/libc.so"; d=open(p,"rb").read(); assert d[:6] == b"\x7fELF\x02\x02" and struct.unpack_from(">I", d, 48)[0] & 3 == 2'
RUN mkdir -p /opt/nyanjit/sysroots/ppc64 && \
    cp -a /opt/nyanjit/toolchains/powerpc64-linux-musl-cross/powerpc64-linux-musl/. /opt/nyanjit/sysroots/ppc64/ && \
    mkdir -p /opt/nyanjit/sysroots/ppc64/lib64 && \
    ln -s ../lib/libc.so /opt/nyanjit/sysroots/ppc64/lib64/ld64.so.2 && \
    ln -sfn libc.so /opt/nyanjit/sysroots/ppc64/lib/ld-musl-powerpc64.so.1

FROM --platform=$BUILDPLATFORM golang:1.27.2 AS build
ARG TARGETOS TARGETARCH
WORKDIR /src
COPY . .
RUN cd e2e && CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -o /nyanjit-e2e ./cmd/nyanjit-e2e

FROM --platform=$BUILDPLATFORM golang:1.27.2 AS vm-compat-probes
COPY e2e/container/compat_probe.go /tmp/compat_probe.go
RUN mkdir /probes && \
    CGO_ENABLED=0 GOOS=freebsd GOARCH=386 go build -o /probes/freebsd-386 /tmp/compat_probe.go && \
    CGO_ENABLED=0 GOOS=freebsd GOARCH=arm GOARM=7 go build -o /probes/freebsd-arm /tmp/compat_probe.go && \
    CGO_ENABLED=0 GOOS=netbsd GOARCH=386 go build -o /probes/netbsd-386 /tmp/compat_probe.go && \
    CGO_ENABLED=0 GOOS=netbsd GOARCH=arm GOARM=7 go build -o /probes/netbsd-arm /tmp/compat_probe.go

FROM --platform=$BUILDPLATFORM debian:trixie AS vm-image-builder
ARG VM_OSES=openbsd,freebsd,netbsd,dragonfly,omnios
ENV DEBIAN_FRONTEND=noninteractive
RUN apt-get update && apt-get install -y --no-install-recommends \
    python3 python3-pexpect && \
    if [ -n "$VM_OSES" ]; then apt-get install -y --no-install-recommends \
      ca-certificates curl openssh-client signify-openbsd \
      qemu-system-x86 qemu-system-arm qemu-system-misc qemu-efi-aarch64 \
      qemu-efi-arm ovmf qemu-utils u-boot-qemu xorriso xz-utils bzip2; fi && \
    rm -rf /var/lib/apt/lists/*
COPY e2e/container/vm_media.py /usr/local/bin/vm_media.py
# Official release key: https://cdn.openbsd.org/pub/OpenBSD/7.9/openbsd-79-base.pub
# Its base64 body is also published at https://www.openbsd.org/79.html.
COPY e2e/container/openbsd-79-base.pub /usr/local/share/nyanjit/openbsd-79-base.pub

FROM vm-image-builder AS openbsd-amd64
ARG VM_OSES=openbsd,freebsd,netbsd,dragonfly,omnios
ARG OPENBSD_ARCHS=amd64,i386,arm64,riscv64,armv7,powerpc64
ARG OPENBSD_AMD64_SHA256SIG=99db2ba3d63cddeeb9a1166319e48bf4b22224c48bbdc4deff8ecadc0f7b2786
RUN python3 /usr/local/bin/vm_media.py openbsd amd64 --manifest-hash "$OPENBSD_AMD64_SHA256SIG" --selection "$OPENBSD_ARCHS" --os-selection "$VM_OSES"
COPY e2e/container/vm_image.py /usr/local/bin/vm_image.py
RUN python3 /usr/local/bin/vm_image.py build-openbsd amd64 --selection "$OPENBSD_ARCHS" --os-selection "$VM_OSES"

FROM vm-image-builder AS openbsd-i386
ARG VM_OSES=openbsd,freebsd,netbsd,dragonfly,omnios
ARG OPENBSD_ARCHS=amd64,i386,arm64,riscv64,armv7,powerpc64
ARG OPENBSD_I386_SHA256SIG=bd1cf42b5f199cf330c35f63802e0be093ef830df2dc149b702fa5c7da445a13
RUN python3 /usr/local/bin/vm_media.py openbsd i386 --manifest-hash "$OPENBSD_I386_SHA256SIG" --selection "$OPENBSD_ARCHS" --os-selection "$VM_OSES"
COPY e2e/container/vm_image.py /usr/local/bin/vm_image.py
RUN python3 /usr/local/bin/vm_image.py build-openbsd i386 --selection "$OPENBSD_ARCHS" --os-selection "$VM_OSES"

FROM vm-image-builder AS openbsd-arm64-media
ARG VM_OSES=openbsd,freebsd,netbsd,dragonfly,omnios
ARG OPENBSD_ARCHS=amd64,i386,arm64,riscv64,armv7,powerpc64
ARG OPENBSD_ARM64_SHA256SIG=a2c36265c14dcf35f16ca6df9f8529991c3e0cf8a0f49f6ec0ed46b102bc4ed0
RUN python3 /usr/local/bin/vm_media.py openbsd arm64 --manifest-hash "$OPENBSD_ARM64_SHA256SIG" --selection "$OPENBSD_ARCHS" --os-selection "$VM_OSES"
FROM openbsd-arm64-media AS openbsd-arm64
COPY e2e/container/vm_image.py /usr/local/bin/vm_image.py
RUN python3 /usr/local/bin/vm_image.py build-openbsd arm64 --selection "$OPENBSD_ARCHS" --os-selection "$VM_OSES"

FROM vm-image-builder AS openbsd-riscv64-media
ARG VM_OSES=openbsd,freebsd,netbsd,dragonfly,omnios
ARG OPENBSD_ARCHS=amd64,i386,arm64,riscv64,armv7,powerpc64
ARG OPENBSD_RISCV64_SHA256SIG=4f8dff676d1ecdaac7fea5f7fd521b521220fc2077dc4e64c9a3c774e53f9e46
RUN python3 /usr/local/bin/vm_media.py openbsd riscv64 --manifest-hash "$OPENBSD_RISCV64_SHA256SIG" --selection "$OPENBSD_ARCHS" --os-selection "$VM_OSES"
FROM openbsd-riscv64-media AS openbsd-riscv64
COPY e2e/container/vm_image.py /usr/local/bin/vm_image.py
RUN python3 /usr/local/bin/vm_image.py build-openbsd riscv64 --selection "$OPENBSD_ARCHS" --os-selection "$VM_OSES"

FROM vm-image-builder AS openbsd-armv7-media
ARG VM_OSES=openbsd,freebsd,netbsd,dragonfly,omnios
ARG OPENBSD_ARCHS=amd64,i386,arm64,riscv64,armv7,powerpc64
ARG OPENBSD_ARMV7_SHA256SIG=59022135f7e8d997f9965b3433ee3a8cf6faa4fa8ee864486e10267b84f541bb
RUN python3 /usr/local/bin/vm_media.py openbsd armv7 --manifest-hash "$OPENBSD_ARMV7_SHA256SIG" --selection "$OPENBSD_ARCHS" --os-selection "$VM_OSES"
FROM openbsd-armv7-media AS openbsd-armv7
RUN if [ -n "$VM_OSES" ]; then apt-get update && apt-get install -y --no-install-recommends dosfstools mtools; fi && rm -rf /var/lib/apt/lists/*
COPY e2e/container/vm_image.py /usr/local/bin/vm_image.py
RUN python3 /usr/local/bin/vm_image.py build-openbsd armv7 --selection "$OPENBSD_ARCHS" --os-selection "$VM_OSES" --experimental

FROM vm-image-builder AS openbsd-powerpc64-media
ARG VM_OSES=openbsd,freebsd,netbsd,dragonfly,omnios
ARG OPENBSD_ARCHS=amd64,i386,arm64,riscv64,armv7,powerpc64
ARG OPENBSD_POWERPC64_SHA256SIG=ac826b9290d6a4fb25a2fb2a32e3053fe44f631f7bd51e57ae404d754c2d9cb9
RUN python3 /usr/local/bin/vm_media.py openbsd powerpc64 --manifest-hash "$OPENBSD_POWERPC64_SHA256SIG" --selection "$OPENBSD_ARCHS" --os-selection "$VM_OSES"
FROM openbsd-powerpc64-media AS openbsd-powerpc64
RUN if [ -n "$VM_OSES" ]; then apt-get update && apt-get install -y --no-install-recommends qemu-system-ppc; fi && rm -rf /var/lib/apt/lists/*
COPY e2e/container/vm_image.py /usr/local/bin/vm_image.py
RUN python3 /usr/local/bin/vm_image.py build-openbsd powerpc64 --selection "$OPENBSD_ARCHS" --os-selection "$VM_OSES" --experimental

FROM vm-image-builder AS freebsd-amd64-media
ARG VM_OSES=openbsd,freebsd,netbsd,dragonfly,omnios
ARG FREEBSD_AMD64_SHA512=bea82b91a983a20eb7ecdc6f11c1006d394ebbc5668b7f0ecdecf5a54fc5f9ea3a8384fc9f74a6611a9b08c183fa953327147ebfaa0de199fd015e67dd1e608a
RUN python3 /usr/local/bin/vm_media.py freebsd amd64 --manifest-hash "$FREEBSD_AMD64_SHA512" --selection "$VM_OSES"
FROM freebsd-amd64-media AS freebsd-amd64
COPY --from=vm-compat-probes /probes/freebsd-386 /usr/local/share/nyanjit/probes/freebsd-386
COPY e2e/container/vm_image.py /usr/local/bin/vm_image.py
RUN python3 /usr/local/bin/vm_image.py build-cloud freebsd-amd64 --selection "$VM_OSES"

FROM vm-image-builder AS freebsd-arm64-media
ARG VM_OSES=openbsd,freebsd,netbsd,dragonfly,omnios
ARG FREEBSD_ARM64_SHA512=df37de822fffdf19d7dddfdd3947390b5ae4dcd7f18a5ef6c6e6bb22effd52b593e09970c85ae081a2bb8290bad99a0fa5e473aed41be332a47bbac62c62bfb4
RUN python3 /usr/local/bin/vm_media.py freebsd arm64 --manifest-hash "$FREEBSD_ARM64_SHA512" --selection "$VM_OSES"
FROM freebsd-arm64-media AS freebsd-arm64
COPY --from=vm-compat-probes /probes/freebsd-arm /usr/local/share/nyanjit/probes/freebsd-arm
COPY e2e/container/vm_image.py /usr/local/bin/vm_image.py
RUN python3 /usr/local/bin/vm_image.py build-cloud freebsd-arm64 --selection "$VM_OSES"

FROM vm-image-builder AS omnios-amd64-media
ARG VM_OSES=openbsd,freebsd,netbsd,dragonfly,omnios
ARG OMNIOS_SHA256=eb36b84bbbbede5b4e7dfed931fe58d29049c02170a8e10dc7e2162d89c97f8f
RUN python3 /usr/local/bin/vm_media.py omnios amd64 --manifest-hash "$OMNIOS_SHA256" --selection "$VM_OSES"
FROM omnios-amd64-media AS omnios-amd64
COPY e2e/container/vm_image.py /usr/local/bin/vm_image.py
RUN python3 /usr/local/bin/vm_image.py build-cloud omnios-amd64 --selection "$VM_OSES"

FROM vm-image-builder AS netbsd-amd64-media
ARG VM_OSES=openbsd,freebsd,netbsd,dragonfly,omnios
ARG NETBSD_AMD64_SHA512=ad0bc2786b2cd8b7c140ae108fd780064465fe06337a3a2c3f3b694ed3a1e042b3d401c9a682be02c081f3eb3efeba1246cfd6a8803149468ebfbd9c0e7c77e8
RUN python3 /usr/local/bin/vm_media.py netbsd amd64 --manifest-hash "$NETBSD_AMD64_SHA512" --selection "$VM_OSES"
FROM netbsd-amd64-media AS netbsd-amd64
COPY --from=vm-compat-probes /probes/netbsd-386 /usr/local/share/nyanjit/probes/netbsd-386
COPY e2e/container/vm_image.py /usr/local/bin/vm_image.py
RUN python3 /usr/local/bin/vm_image.py build-netbsd amd64 --selection "$VM_OSES"

FROM vm-image-builder AS netbsd-arm64-media
ARG VM_OSES=openbsd,freebsd,netbsd,dragonfly,omnios
ARG NETBSD_ARM64_SHA512=ce7f952173412779ed3d990c9de69e36f8440e2a6849cace8c6cd3894c894b1a48c138b5faf0d500b263f6e7ef3b962dbe52ba7f350575b3acef64fc3fd827f7
RUN python3 /usr/local/bin/vm_media.py netbsd arm64 --manifest-hash "$NETBSD_ARM64_SHA512" --selection "$VM_OSES"
FROM netbsd-arm64-media AS netbsd-arm64
COPY e2e/container/vm_image.py /usr/local/bin/vm_image.py
RUN python3 /usr/local/bin/vm_image.py build-netbsd arm64 --selection "$VM_OSES"

FROM vm-image-builder AS netbsd-arm-media
ARG VM_OSES=openbsd,freebsd,netbsd,dragonfly,omnios
ARG NETBSD_ARM_SHA512=32f511ed7bddf2565e210c60549fd3fd3a45e6c29567c358f386c9397ba7879809f4be98f1ab5810df057912ea19040e456928244b481f91530a33158831c2a6
RUN python3 /usr/local/bin/vm_media.py netbsd arm --manifest-hash "$NETBSD_ARM_SHA512" --selection "$VM_OSES"
FROM netbsd-arm-media AS netbsd-arm
COPY e2e/container/vm_image.py /usr/local/bin/vm_image.py
RUN python3 /usr/local/bin/vm_image.py build-netbsd arm --selection "$VM_OSES"

FROM vm-image-builder AS dragonfly-amd64-media
ARG VM_OSES=openbsd,freebsd,netbsd,dragonfly,omnios
# The upstream mirror publishes MD5 for this legacy release; the digest is pinned here.
ARG DRAGONFLY_AMD64_MD5=d321c363be7653525049c501ecf16e37
RUN python3 /usr/local/bin/vm_media.py dragonfly amd64 --manifest-hash "$DRAGONFLY_AMD64_MD5" --selection "$VM_OSES"
FROM dragonfly-amd64-media AS dragonfly-amd64
ARG DRAGONFLY_AMD64_SHA256=f1c1fb4c2aff3537b4fd03dc47cc2173966cbabf7c37ec4af0f9ab7f3b9a91eb
RUN if [ -f /work/media/dragonfly-amd64/dfly-x86_64-6.4.2_REL.img.bz2 ]; then \
      echo "$DRAGONFLY_AMD64_SHA256  /work/media/dragonfly-amd64/dfly-x86_64-6.4.2_REL.img.bz2" | sha256sum -c -; fi
COPY e2e/container/vm_image.py /usr/local/bin/vm_image.py
RUN python3 /usr/local/bin/vm_image.py build-dragonfly amd64 --selection "$VM_OSES"

FROM debian:trixie
ARG TARGETARCH
ARG LINUX_ONLY=false
ENV DEBIAN_FRONTEND=noninteractive
RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates curl python3 openssh-client util-linux procps \
    qemu-user-static gcc-powerpc64le-linux-gnu libc6-dev-ppc64el-cross && \
    if [ "$LINUX_ONLY" != true ]; then apt-get install -y --no-install-recommends \
      qemu-system-x86 qemu-system-arm qemu-system-misc qemu-system-ppc \
      qemu-efi-aarch64 qemu-efi-arm ovmf u-boot-qemu qemu-utils zstd; fi && \
    rm -rf /var/lib/apt/lists/*
RUN if [ "$LINUX_ONLY" = true ]; then :; elif [ "$TARGETARCH" = arm64 ]; then \
      curl -fL --retry 4 -o /tmp/hangover.tar \
        https://github.com/AndreRH/hangover/releases/download/hangover-11.16/hangover_11.16_debian13_trixie_arm64.tar && \
      echo 'b5493f5903ab3c05f78bf4c03b06ac0005eb26f732437748c1592fefa45e861a  /tmp/hangover.tar' | sha256sum -c - && \
      mkdir /tmp/hangover && tar xf /tmp/hangover.tar -C /tmp/hangover --wildcards '*.deb' && \
      apt-get update && apt-get install -y --no-install-recommends /tmp/hangover/*.deb && \
      rm -rf /tmp/hangover /tmp/hangover.tar; \
    else \
      dpkg --add-architecture i386 && apt-get update && \
      apt-get install -y --no-install-recommends wine wine64 wine32:i386; \
    fi && rm -rf /var/lib/apt/lists/*
# The image's native QEMU builds carry the F-compatible binfmt installer.
COPY --from=binfmt /usr/bin/ /usr/bin/
COPY --from=libc-amd64 /usr/lib/x86_64-linux-gnu/ /usr/lib/x86_64-linux-gnu/
COPY --from=libc-amd64 /lib64/ld-linux-x86-64.so.2 /lib64/ld-linux-x86-64.so.2
COPY --from=libc-i386 /usr/lib/i386-linux-gnu/ /usr/lib/i386-linux-gnu/
COPY --from=libc-i386 /lib/ld-linux.so.2 /lib/ld-linux.so.2
COPY --from=libc-armhf /usr/lib/arm-linux-gnueabihf/ /usr/lib/arm-linux-gnueabihf/
COPY --from=libc-armhf /lib/ld-linux-armhf.so.3 /lib/ld-linux-armhf.so.3
COPY --from=libc-ppc64le /usr/lib/powerpc64le-linux-gnu/ /usr/lib/powerpc64le-linux-gnu/
COPY --from=libc-ppc64le /usr/lib/powerpc64le-linux-gnu/ /opt/nyanjit/sysroots/ppc64le/usr/lib/powerpc64le-linux-gnu/
COPY --from=libc-ppc64le /lib64/ld64.so.2 /opt/nyanjit/sysroots/ppc64le/lib64/ld64.so.2
RUN mkdir -p /opt/nyanjit/sysroots/ppc64le/lib && \
    ln -s ../usr/lib/powerpc64le-linux-gnu /opt/nyanjit/sysroots/ppc64le/lib/powerpc64le-linux-gnu
COPY --from=libc-ppc64-elfv2 /opt/nyanjit/ /opt/nyanjit/
COPY --from=libc-riscv64 /usr/lib/riscv64-linux-gnu/ /usr/lib/riscv64-linux-gnu/
COPY --from=libc-riscv64 /lib/ld-linux-riscv64-lp64d.so.1 /lib/ld-linux-riscv64-lp64d.so.1
RUN ln -s ld-linux-riscv64-lp64d.so.1 /lib/ld.so.1
COPY --from=go126 /usr/local/go/ /opt/go1.26.9/
COPY --from=go127 /usr/local/go/ /opt/go1.27.2/
COPY --from=build /nyanjit-e2e /usr/local/bin/nyanjit-e2e
COPY e2e/container/entrypoint.sh /usr/local/bin/nyanjit-e2e-entrypoint
RUN chmod +x /usr/local/bin/nyanjit-e2e-entrypoint
COPY --from=openbsd-amd64 /vm/ /opt/nyanjit/vms/openbsd-7.9-amd64/
COPY --from=openbsd-i386 /vm/ /opt/nyanjit/vms/openbsd-7.9-i386/
COPY --from=openbsd-arm64 /vm/ /opt/nyanjit/vms/openbsd-7.9-arm64/
COPY --from=openbsd-riscv64 /vm/ /opt/nyanjit/vms/openbsd-7.9-riscv64/
COPY --from=openbsd-armv7 /vm/ /opt/nyanjit/vms/openbsd-7.9-arm/
COPY --from=openbsd-powerpc64 /vm/ /opt/nyanjit/vms/openbsd-7.9-ppc64/
COPY --from=freebsd-amd64 /vm/ /opt/nyanjit/vms/freebsd-15.1-amd64/
COPY --from=freebsd-arm64 /vm/ /opt/nyanjit/vms/freebsd-15.1-arm64/
COPY --from=netbsd-amd64 /vm/ /opt/nyanjit/vms/netbsd-11.0-amd64/
COPY --from=netbsd-arm64 /vm/ /opt/nyanjit/vms/netbsd-11.0-arm64/
COPY --from=netbsd-arm /vm/ /opt/nyanjit/vms/netbsd-11.0-arm/
COPY --from=dragonfly-amd64 /vm/ /opt/nyanjit/vms/dragonfly-6.4.2-amd64/
COPY --from=omnios-amd64 /vm/ /opt/nyanjit/vms/omnios-r151058-amd64/
ENV PATH=/opt/go1.27.2/bin:/usr/local/bin:/usr/bin:/bin
WORKDIR /src
ENTRYPOINT ["/usr/local/bin/nyanjit-e2e-entrypoint"]
CMD ["nyanjit-e2e", "e2e/matrix.star"]
