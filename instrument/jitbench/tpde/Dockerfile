# Native arm64 build/run environment for the TPDE-LLVM benchmark harness.
# The local debian:trixie tag is an amd64 image; debian:stable-slim here is
# the arm64 Debian 13 (trixie) image, so it is used as the trixie base.
FROM debian:stable-slim
RUN apt-get update && apt-get install -y --no-install-recommends \
        clang-19 llvm-19 llvm-19-dev llvm-19-tools libclang-rt-19-dev \
        libzstd-dev zlib1g-dev libxml2-dev libedit-dev libffi-dev libcurl4-openssl-dev \
        cmake ninja-build git binutils util-linux ca-certificates python3 \
    && rm -rf /var/lib/apt/lists/*
ENV PATH=/usr/lib/llvm-19/bin:$PATH
WORKDIR /work
