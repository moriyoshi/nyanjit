# Independent C stack-alignment fixture

`aligned_local.c` uses a volatile, 16-byte-aligned `unsigned __int128` local.
The checked-in hex files contain only its relocation-free `.text` section.
Tests map those bytes as a C callee, independently of NyanJIT's encoder.
They check direct foreign calls, generated foreign calls, and managed calls
from both C and Go entries. No C toolchain is needed when running tests.

Regeneration (then hex-encode the `.bin` file with Python's `bytes.hex()`):

```
loongarch64-unknown-linux-musl-gcc -O2 -march=loongarch64 -mno-lsx -mno-lasx \
  -ffreestanding -fno-pic -fno-stack-protector -fno-asynchronous-unwind-tables \
  -c aligned_local.c -o aligned_local.o
loongarch64-unknown-linux-musl-objdump -dr aligned_local.o
loongarch64-unknown-linux-musl-objcopy -O binary -j .text aligned_local.o aligned_local.bin

riscv64-linux-gnu-gcc -O2 -march=rv64gc -mabi=lp64d -mno-relax \
  -ffreestanding -fno-pic -fno-stack-protector -fno-asynchronous-unwind-tables \
  -c aligned_local.c -o aligned_local.o
riscv64-linux-gnu-objdump -dr aligned_local.o
riscv64-linux-gnu-objcopy -O binary -j .text aligned_local.o aligned_local.bin
```

Verify that objdump reports no relocations before updating the fixture.

The loong64 fixture was compiled with GCC 14.4.0 from the pinned musl cross
compiler already used by the E2E container: `loong64/cross-tools`, release
`20260912`, ARM64 archive SHA256
`3f4c46cc5b7247f5498ce3b3d48e4f923cade5fe13859839d806504cfc82a8ce`.
The RISC-V fixture was compiled in a disposable Debian trixie container with
`gcc-14-riscv64-linux-gnu` version `14.2.0-19cross1` and
`binutils-riscv64-linux-gnu` version `2.44-3`.
